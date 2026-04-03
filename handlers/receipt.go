package handlers

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/gif"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"golang.org/x/image/draw"
)

const (
	maxReceiptBytes  = 20 << 20 // 20 MB
	anthropicAPI     = "https://api.anthropic.com/v1/messages"
	anthropicModel   = "claude-sonnet-4-6"
	anthropicVersion = "2023-06-01"
)

// ReceiptItem is a single line item parsed from a receipt image.
type ReceiptItem struct {
	Name            string  `json:"name"`
	PLU             string  `json:"plu"`
	Quantity        float64 `json:"quantity"`
	Unit            string  `json:"unit"`
	UnitCostCents   int64   `json:"unit_cost_cents"`
	QuantityPerScan float64 `json:"quantity_per_scan"` // 0 means not set
}

func RegisterReceipt(mux *http.ServeMux, db *sql.DB) {
	mux.HandleFunc("POST /api/receipt/parse", func(w http.ResponseWriter, r *http.Request) {
		handleReceiptParse(w, r, db)
	})
}

func handleReceiptParse(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	if err := r.ParseMultipartForm(maxReceiptBytes); err != nil {
		WriteError(w, http.StatusBadRequest, "file too large (max 20 MB)")
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		WriteError(w, http.StatusBadRequest, "missing file field")
		return
	}
	defer file.Close()

	imgBytes, err := io.ReadAll(io.LimitReader(file, maxReceiptBytes))
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "could not read file")
		return
	}

	mediaType := hdr.Header.Get("Content-Type")
	if mediaType == "" {
		mediaType = http.DetectContentType(imgBytes)
	}
	if !strings.HasPrefix(mediaType, "image/") {
		WriteError(w, http.StatusBadRequest, "file must be an image")
		return
	}

	items, err := parseReceiptWithClaude(imgBytes, mediaType)
	if err != nil {
		WriteError(w, http.StatusBadGateway, "receipt parsing failed: "+err.Error())
		return
	}

	// For any item with a PLU/barcode, look up the existing inventory name and quantity_per_scan.
	for i, item := range items {
		if item.PLU == "" {
			continue
		}
		var existingName string
		var qtyPerScan float64
		err := db.QueryRow(`SELECT name, quantity_per_scan FROM inventory WHERE barcode=? LIMIT 1`, item.PLU).Scan(&existingName, &qtyPerScan)
		if err == nil {
			items[i].Name = existingName
			items[i].QuantityPerScan = qtyPerScan
		}
	}

	WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

// prepareForAPI converts the image to grayscale (receipts are black-and-white,
// so color is wasted bytes) and scales down only if still over 4 MB after that.
// Grayscale typically cuts file size by ~60-70%, letting us send higher resolution.
func prepareForAPI(imgBytes []byte) ([]byte, string, error) {
	const maxBytes = 4 << 20 // 4 MB — safely under Anthropic's 5 MB limit

	img, _, err := image.Decode(bytes.NewReader(imgBytes))
	if err != nil {
		return nil, "", fmt.Errorf("could not decode image: %w", err)
	}

	// Convert to grayscale.
	bounds := img.Bounds()
	gray := image.NewGray(bounds)
	draw.Draw(gray, bounds, img, bounds.Min, draw.Src)

	// Encode grayscale at high quality and check size.
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, gray, &jpeg.Options{Quality: 90}); err != nil {
		return nil, "", fmt.Errorf("could not encode image: %w", err)
	}
	if buf.Len() <= maxBytes {
		return buf.Bytes(), "image/jpeg", nil
	}

	// Still too large — scale down proportionally until it fits.
	w, h := bounds.Dx(), bounds.Dy()
	scale := 0.75
	for {
		nw := int(float64(w) * scale)
		nh := int(float64(h) * scale)
		dst := image.NewGray(image.Rect(0, 0, nw, nh))
		draw.BiLinear.Scale(dst, dst.Bounds(), gray, bounds, draw.Over, nil)

		buf.Reset()
		if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 85}); err != nil {
			return nil, "", fmt.Errorf("could not encode resized image: %w", err)
		}
		if buf.Len() <= maxBytes {
			return buf.Bytes(), "image/jpeg", nil
		}
		if scale < 0.1 {
			return nil, "", fmt.Errorf("image could not be compressed below 4 MB; please use a smaller image")
		}
		scale *= 0.5
	}
}

func parseReceiptWithClaude(imgBytes []byte, mediaType string) ([]ReceiptItem, error) {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("ANTHROPIC_API_KEY not set")
	}

	var err error
	imgBytes, mediaType, err = prepareForAPI(imgBytes)
	if err != nil {
		return nil, err
	}

	b64 := base64.StdEncoding.EncodeToString(imgBytes)

	prompt := `You are an expert grocery receipt parser. Extract every PURCHASED ITEM from this receipt image.

Read top to bottom. Identify these line types:
- Purchased item: has a product name, optional barcode/PLU, and a positive dollar amount
- Weighted item: shows weight + per-unit price (e.g. "2.431 LB @ 4.99 USD/LB")
- Multi-quantity: count prefix before price (e.g. "2 @")
- Discount/savings line: shows a NEGATIVE amount or words like "SAVE", "DISC", "COUPON", "MEMBER", "REWARDS" — these are NOT separate items, they reduce the cost of the item above them
- Tax, subtotal, total, payment, loyalty card lines — ignore these entirely

For each PURCHASED ITEM output:
- "name": clean lowercase name. Expand abbreviations (e.g. "CKN BREA" → "chicken breast", "B/S" → "boneless skinless", "HYV" → "hy-vee", "CHOB" → "chobani", "LD" → "store brand"). Keep brand names where recognizable.
- "plu": PLU, UPC, or barcode as a string if visible, else ""
- "quantity": For weighted items use the actual weight shown (e.g. 2.431). For multi-packs use the count. Default 1.
- "unit": ONLY use a weight unit (lb, oz, kg, g) if the receipt explicitly shows a weight measurement for that item. Otherwise use "piece" for ALL packaged goods regardless of what type of product it is.
- "unit_cost_cents": integer cents. For weighted items use the per-weight-unit rate (e.g. "4.99/LB" → 499). For packaged items: if a discount line follows this item, subtract it from the line total before dividing by quantity. Round to nearest cent.

Critical rules:
- NEVER create a separate item for a discount, savings, or coupon line — apply it to the item above
- NEVER assume a unit based on product type — only use weight units when the receipt explicitly states a weight
- Include every purchased item, even ones you are unsure about — make your best guess on the name
- Do not include tax, totals, subtotals, or payment method lines

Return ONLY a valid JSON array, no markdown, no explanation.
Example: [{"name":"fairlife 2% milk","plu":"4902100277","quantity":1,"unit":"piece","unit_cost_cents":532},{"name":"chicken breast boneless skinless","plu":"3077600000","quantity":2.431,"unit":"lb","unit_cost_cents":499}]`

	reqBody := map[string]any{
		"model":      anthropicModel,
		"max_tokens": 4096,
		"messages": []map[string]any{
			{
				"role": "user",
				"content": []map[string]any{
					{
						"type": "image",
						"source": map[string]any{
							"type":       "base64",
							"media_type": mediaType,
							"data":       b64,
						},
					},
					{
						"type": "text",
						"text": prompt,
					},
				},
			},
		},
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, anthropicAPI, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", anthropicVersion)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("API request failed: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("could not read API response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned %d: %s", resp.StatusCode, string(respBytes))
	}

	// Parse Anthropic response envelope
	var apiResp struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(respBytes, &apiResp); err != nil {
		return nil, fmt.Errorf("could not parse API response: %w", err)
	}
	if len(apiResp.Content) == 0 {
		return nil, fmt.Errorf("empty response from API")
	}

	text := strings.TrimSpace(apiResp.Content[0].Text)

	// Strip markdown code fences if present
	if strings.HasPrefix(text, "```") {
		if idx := strings.Index(text, "\n"); idx >= 0 {
			text = text[idx+1:]
		}
		text = strings.TrimSuffix(strings.TrimSpace(text), "```")
	}

	var items []ReceiptItem
	if err := json.Unmarshal([]byte(text), &items); err != nil {
		return nil, fmt.Errorf("could not parse items JSON: %w — raw: %s", err, text[:min(200, len(text))])
	}

	return items, nil
}
