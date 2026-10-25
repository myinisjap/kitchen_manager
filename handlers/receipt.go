package handlers

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"golang.org/x/image/draw"
)

const (
	maxReceiptBytes       = 20 << 20 // 20 MB
	anthropicAPI          = "https://api.anthropic.com/v1/messages"
	defaultAnthropicModel = "claude-sonnet-5-5"
	anthropicModelEnv     = "ANTHROPIC_MODEL"
	receiptMaxTokens      = 8192
	receiptToolName       = "record_receipt_items"
	receiptAPITimeout     = 90 * time.Second
	anthropicVersion      = "2023-06-01"
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
	r.Body = http.MaxBytesReader(w, r.Body, maxReceiptUploadBytes)
	if err := r.ParseMultipartForm(maxReceiptBytes); err != nil {
		if isBodyTooLarge(err) {
			WriteError(w, http.StatusRequestEntityTooLarge, "file too large (max 20 MB)")
		} else {
			WriteError(w, http.StatusBadRequest, "invalid multipart form")
		}
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

	if err := checkImageDimensions(imgBytes); err != nil {
		if errors.Is(err, errImageTooLarge) {
			WriteError(w, http.StatusRequestEntityTooLarge, "image dimensions too large (max 50 megapixels)")
		} else {
			WriteError(w, http.StatusBadRequest, "could not read image (supported: JPEG, PNG, GIF)")
		}
		return
	}

	items, err := parseReceiptWithClaude(imgBytes, mediaType)
	if err != nil {
		WriteError(w, http.StatusBadGateway, "receipt parsing failed: "+err.Error())
		return
	}

	enrichReceiptItems(db, items)

	WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

// prepareForAPI converts the image to grayscale (receipts are black-and-white,
// so color is wasted bytes) and scales down only if still over 4 MB after that.
// Grayscale typically cuts file size by ~60-70%, letting us send higher resolution.
func prepareForAPI(imgBytes []byte) ([]byte, string, error) {
	const maxBytes = 4 << 20 // 4 MB — safely under Anthropic's 5 MB limit

	if err := checkImageDimensions(imgBytes); err != nil {
		return nil, "", err
	}
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

const receiptPrompt = `You are an expert grocery receipt parser. Extract every PURCHASED ITEM from this receipt image.

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

Call the record_receipt_items tool exactly once with every purchased item in the "items" array.
Example items: [{"name":"fairlife 2% milk","plu":"4902100277","quantity":1,"unit":"piece","unit_cost_cents":532},{"name":"chicken breast boneless skinless","plu":"3077600000","quantity":2.431,"unit":"lb","unit_cost_cents":499}]`

var errReceiptTruncated = errors.New("receipt too long: the model's output was cut off before all items were returned; try photographing the receipt in smaller sections")

func receiptModel() string {
	if m := strings.TrimSpace(os.Getenv(anthropicModelEnv)); m != "" {
		return m
	}
	return defaultAnthropicModel
}

func receiptToolSchema() map[string]any {
	return map[string]any{
		"name":        receiptToolName,
		"description": "Record every purchased line item parsed from the receipt.",
		"input_schema": map[string]any{
			"type":     "object",
			"required": []string{"items"},
			"properties": map[string]any{
				"items": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type":     "object",
						"required": []string{"name", "plu", "quantity", "unit", "unit_cost_cents"},
						"properties": map[string]any{
							"name":            map[string]any{"type": "string"},
							"plu":             map[string]any{"type": "string", "description": "PLU/UPC/barcode if visible, else empty string"},
							"quantity":        map[string]any{"type": "number"},
							"unit":            map[string]any{"type": "string", "description": "lb, oz, kg, g only when a weight is printed; otherwise piece"},
							"unit_cost_cents": map[string]any{"type": "integer"},
						},
					},
				},
			},
		},
	}
}

func buildReceiptRequest(model, mediaType, b64 string) map[string]any {
	return map[string]any{
		"model":       model,
		"max_tokens":  receiptMaxTokens,
		"tools":       []any{receiptToolSchema()},
		"tool_choice": map[string]any{"type": "tool", "name": receiptToolName},
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
					{"type": "text", "text": receiptPrompt},
				},
			},
		},
	}
}

func parseReceiptResponse(respBytes []byte) ([]ReceiptItem, error) {
	var env struct {
		StopReason string `json:"stop_reason"`
		Content    []struct {
			Type  string          `json:"type"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
			Text  string          `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(respBytes, &env); err != nil {
		return nil, fmt.Errorf("could not parse API response: %w", err)
	}
	if env.StopReason == "max_tokens" {
		return nil, errReceiptTruncated
	}
	var input json.RawMessage
	text := ""
	for _, c := range env.Content {
		if c.Type == "tool_use" && c.Name == receiptToolName {
			input = c.Input
			break
		}
		if c.Type == "text" && text == "" {
			text = c.Text
		}
	}
	if input == nil {
		return nil, fmt.Errorf("model did not return structured items (stop_reason=%q): %s", env.StopReason, text[:min(200, len(text))])
	}
	var out struct {
		Items []ReceiptItem `json:"items"`
	}
	if err := json.Unmarshal(input, &out); err != nil {
		return nil, fmt.Errorf("could not parse items: %w", err)
	}
	if out.Items == nil {
		return []ReceiptItem{}, nil
	}
	for i := range out.Items {
		it := &out.Items[i]
		it.Name = strings.TrimSpace(it.Name)
		it.PLU = strings.TrimSpace(it.PLU)
		it.Unit = strings.TrimSpace(it.Unit)
		it.QuantityPerScan = 0
	}
	return out.Items, nil
}

// lookupReceiptPLU checks inventory.barcode, then inventory_skus aliases.
func lookupReceiptPLU(db *sql.DB, plu string) (string, float64, bool, error) {
	var name string
	var qps float64
	err := db.QueryRow(`SELECT name, quantity_per_scan FROM inventory WHERE barcode=? LIMIT 1`, plu).Scan(&name, &qps)
	if err == nil {
		return name, qps, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", 0, false, err
	}
	err = db.QueryRow(`SELECT i.name, s.quantity_per_scan FROM inventory_skus s JOIN inventory i ON i.id = s.inventory_id WHERE s.barcode=? LIMIT 1`, plu).Scan(&name, &qps)
	if err == nil {
		return name, qps, true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, false, nil
	}
	return "", 0, false, err
}

func enrichReceiptItems(db *sql.DB, items []ReceiptItem) {
	for i := range items {
		plu := strings.TrimSpace(items[i].PLU)
		if plu == "" {
			continue
		}
		name, qps, ok, err := lookupReceiptPLU(db, plu)
		if err != nil {
			log.Printf("receipt PLU lookup %q: %v", plu, err)
			continue
		}
		if ok {
			items[i].Name = name
			items[i].QuantityPerScan = qps
		}
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

	bodyBytes, err := json.Marshal(buildReceiptRequest(receiptModel(), mediaType, b64))
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), receiptAPITimeout)
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
	return parseReceiptResponse(respBytes)
}
