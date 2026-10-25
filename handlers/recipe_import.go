package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	xhtml "golang.org/x/net/html"
)

// ingredientRangeRe matches quantity ranges like "2-3" at the start of a token.
var ingredientRangeRe = regexp.MustCompile(`\b(\d+(?:\.\d+)?)-\d+(?:\.\d+)?\b`)

const (
	maxFetchBytes = 1 << 20  // 1 MB
	maxPDFBytes   = 10 << 20 // 10 MB
)

// ImportedIngredient is a structured ingredient extracted from schema.org/Recipe JSON-LD.
type ImportedIngredient struct {
	Name     string  `json:"name"`
	Quantity float64 `json:"quantity"`
	Unit     string  `json:"unit"`
}

// ImportedRecipe is a structured recipe extracted from schema.org/Recipe JSON-LD.
type ImportedRecipe struct {
	Name         string               `json:"name"`
	Description  string               `json:"description"`
	Servings     int                  `json:"servings"`
	Instructions string               `json:"instructions"`
	Tags         string               `json:"tags"`
	Ingredients  []ImportedIngredient `json:"ingredients"`
}

// RegisterRecipeImport registers the URL and PDF import handlers on mux.
func RegisterRecipeImport(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/recipes/import/url", handleImportURL)
	mux.HandleFunc("POST /api/recipes/import/html", handleImportHTML)
	mux.HandleFunc("POST /api/recipes/import/pdf", handleImportPDF)
}

// handleImportHTML accepts raw HTML from the browser (which fetched the page itself)
// and runs the same JSON-LD extraction + plain-text fallback.
func handleImportHTML(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxFetchBytes))
	if err != nil || len(bytes.TrimSpace(body)) == 0 {
		WriteError(w, http.StatusBadRequest, "empty body")
		return
	}
	if recipe := extractJSONLD(body); recipe != nil {
		WriteJSON(w, http.StatusOK, map[string]any{"recipe": recipe})
		return
	}
	plainText := stripHTML(bytes.NewReader(body))
	WriteJSON(w, http.StatusOK, map[string]any{"text": plainText})
}

var directFetchClient = newSafeHTTPClient(15 * time.Second)

// fetchURL performs a direct HTTP GET with a browser-like User-Agent via the
// SSRF-safe client (dial-time IP checks cover redirects and DNS rebinding).
// The caller should run validateFetchURL first for a friendly early error.
func fetchURL(ctx context.Context, targetURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, fmt.Errorf("invalid url")
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")

	resp, err := directFetchClient.Do(req)
	if err != nil {
		if errors.Is(err, errBlockedAddress) || errors.Is(err, errBlockedURL) {
			return nil, errBlockedAddress
		}
		return nil, fmt.Errorf("could not fetch url: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("url returned status %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxFetchBytes))
}

func handleImportURL(w http.ResponseWriter, r *http.Request) {
	var input struct {
		URL string `json:"url"`
	}
	if err := ReadJSON(r, &input); err != nil || input.URL == "" {
		WriteError(w, http.StatusBadRequest, "invalid url")
		return
	}
	const blockedMsg = "url points to a private or local network address and cannot be imported"
	if _, err := validateFetchURL(r.Context(), input.URL); err != nil {
		switch {
		case errors.Is(err, errBlockedAddress), errors.Is(err, errBlockedURL):
			WriteError(w, http.StatusBadRequest, blockedMsg)
		case errors.Is(err, errBadScheme):
			WriteError(w, http.StatusBadRequest, "url must use http or https")
		default:
			WriteError(w, http.StatusBadRequest, "could not resolve host")
		}
		return
	}

	body, err := fetchURL(r.Context(), input.URL)
	if err != nil {
		if errors.Is(err, errBlockedAddress) {
			WriteError(w, http.StatusBadRequest, blockedMsg)
			return
		}
		WriteError(w, http.StatusBadGateway, err.Error()+" — try the Paste tab instead.")
		return
	}

	ct := http.DetectContentType(body)
	if strings.Contains(ct, "pdf") {
		WriteError(w, http.StatusBadRequest, "url returned a PDF; use the PDF upload tab instead")
		return
	}

	if isCloudflareBlock(body) {
		WriteError(w, http.StatusBadGateway, "This site blocks automated requests. Use the Paste tab instead.")
		return
	}

	if recipe := extractJSONLD(body); recipe != nil {
		WriteJSON(w, http.StatusOK, map[string]any{"recipe": recipe})
		return
	}

	plainText := stripHTML(bytes.NewReader(body))
	WriteJSON(w, http.StatusOK, map[string]any{"text": plainText})
}

func handleImportPDF(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(maxPDFBytes); err != nil {
		WriteError(w, http.StatusBadRequest, "file too large (max 10 MB)")
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		WriteError(w, http.StatusBadRequest, "missing file field")
		return
	}
	defer file.Close()

	tmpFile, err := os.CreateTemp("", "recipe-import-*.pdf")
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "could not create temp file")
		return
	}
	defer os.Remove(tmpFile.Name())

	if _, err := io.Copy(tmpFile, file); err != nil {
		tmpFile.Close()
		WriteError(w, http.StatusInternalServerError, "could not write temp file")
		return
	}
	tmpFile.Close()

	plainText, err := extractPDFText(tmpFile.Name())
	if err != nil {
		WriteError(w, http.StatusUnprocessableEntity, "could not parse PDF: "+err.Error())
		return
	}
	if strings.TrimSpace(plainText) == "" {
		WriteError(w, http.StatusUnprocessableEntity, "no text found in PDF; image-only PDFs are not supported")
		return
	}

	WriteJSON(w, http.StatusOK, map[string]any{"text": plainText})
}

// isCloudflareBlock returns true if the body looks like a Cloudflare challenge/block page.
func isCloudflareBlock(body []byte) bool {
	s := strings.ToLower(string(body[:min(len(body), 4096)]))
	return (strings.Contains(s, "cloudflare") || strings.Contains(s, "cf-browser-verification")) &&
		(strings.Contains(s, "just a moment") || strings.Contains(s, "browser integrity check") ||
			strings.Contains(s, "browser not supported") || strings.Contains(s, "security verification"))
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// extractJSONLD searches the HTML body for <script type="application/ld+json"> blocks
// containing a schema.org Recipe and returns a structured ImportedRecipe if found.
func extractJSONLD(body []byte) *ImportedRecipe {
	tokenizer := xhtml.NewTokenizer(bytes.NewReader(body))
	for {
		tt := tokenizer.Next()
		if tt == xhtml.ErrorToken {
			break
		}
		if tt != xhtml.StartTagToken {
			continue
		}
		name, hasAttr := tokenizer.TagName()
		if string(name) != "script" {
			continue
		}
		isLDJSON := false
		for hasAttr {
			var k, v []byte
			k, v, hasAttr = tokenizer.TagAttr()
			if string(k) == "type" && string(v) == "application/ld+json" {
				isLDJSON = true
			}
		}
		if !isLDJSON {
			continue
		}
		if tokenizer.Next() != xhtml.TextToken {
			continue
		}
		raw := tokenizer.Text()
		if recipe := parseSchemaRecipe(raw); recipe != nil {
			return recipe
		}
	}
	return nil
}

// parseSchemaRecipe tries to parse raw JSON-LD bytes as a schema.org Recipe.
// It handles both a single object and a @graph array.
func parseSchemaRecipe(raw []byte) *ImportedRecipe {
	// Try as a single object first.
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err == nil {
		if r := schemaObjToRecipe(obj); r != nil {
			return r
		}
		// Try @graph
		if graphRaw, ok := obj["@graph"]; ok {
			var graph []map[string]json.RawMessage
			if err := json.Unmarshal(graphRaw, &graph); err == nil {
				for _, item := range graph {
					if r := schemaObjToRecipe(item); r != nil {
						return r
					}
				}
			}
		}
	}
	// Try as an array at the top level.
	var arr []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &arr); err == nil {
		for _, item := range arr {
			if r := schemaObjToRecipe(item); r != nil {
				return r
			}
		}
	}
	return nil
}

func schemaObjToRecipe(obj map[string]json.RawMessage) *ImportedRecipe {
	typeRaw, ok := obj["@type"]
	if !ok {
		return nil
	}
	// @type can be a string or array of strings.
	if !jsonContainsRecipeType(typeRaw) {
		return nil
	}

	r := &ImportedRecipe{Servings: 1}

	if v := jsonStr(obj["name"]); v != "" {
		r.Name = v
	}
	if r.Name == "" {
		return nil
	}
	if v := jsonStr(obj["description"]); v != "" {
		r.Description = stripHTMLString(v)
	}
	if v := jsonStr(obj["recipeYield"]); v != "" {
		fmt.Sscanf(v, "%d", &r.Servings)
	}
	if r.Servings == 0 {
		r.Servings = 1
	}

	// Keywords → tags
	if v := jsonStr(obj["keywords"]); v != "" {
		r.Tags = v
	}

	// Instructions: recipeInstructions can be string, []string, or []HowToStep
	if raw, ok := obj["recipeInstructions"]; ok {
		r.Instructions = parseInstructions(raw)
	}

	// Ingredients
	if raw, ok := obj["recipeIngredient"]; ok {
		var strs []string
		if err := json.Unmarshal(raw, &strs); err == nil {
			for _, s := range strs {
				ing := parseIngredientString(s)
				log.Printf("ingredient: %q → qty=%.2f unit=%q name=%q", s, ing.Quantity, ing.Unit, ing.Name)
				r.Ingredients = append(r.Ingredients, ing)
			}
		}
	}

	return r
}

func jsonContainsRecipeType(raw json.RawMessage) bool {
	if raw == nil {
		return false
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return strings.EqualFold(s, "recipe") || strings.HasSuffix(strings.ToLower(s), "/recipe")
	}
	var arr []string
	if err := json.Unmarshal(raw, &arr); err == nil {
		for _, v := range arr {
			if strings.EqualFold(v, "recipe") || strings.HasSuffix(strings.ToLower(v), "/recipe") {
				return true
			}
		}
	}
	return false
}

func jsonStr(raw json.RawMessage) string {
	if raw == nil {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	// Sometimes it's a number (e.g. recipeYield: 4)
	var n json.Number
	if err := json.Unmarshal(raw, &n); err == nil {
		return n.String()
	}
	return ""
}

// parseInstructions handles the many shapes recipeInstructions can take.
func parseInstructions(raw json.RawMessage) string {
	// Plain string
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return stripHTMLString(s)
	}
	// Array of strings or HowToStep objects
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		return ""
	}
	var steps []string
	for i, item := range arr {
		var str string
		if err := json.Unmarshal(item, &str); err == nil {
			steps = append(steps, fmt.Sprintf("%d. %s", i+1, stripHTMLString(str)))
			continue
		}
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(item, &obj); err == nil {
			text := jsonStr(obj["text"])
			if text == "" {
				text = jsonStr(obj["name"])
			}
			if text != "" {
				steps = append(steps, fmt.Sprintf("%d. %s", i+1, stripHTMLString(text)))
			}
		}
	}
	return strings.Join(steps, "\n")
}

// normalizeIngredientString replaces unicode vulgar fractions and superscript digits
// with ASCII equivalents so the quantity parser can handle them.
// It also collapses ranges like "2-3" to the lower bound "2".
var mixedFractionRe = regexp.MustCompile(`([0-9])([\x{00bc}-\x{00be}\x{2150}-\x{215e}])`)

func normalizeIngredientString(s string) string {
	// Separate a mixed number like "1½" into "1 ½" so it doesn't become "11/2".
	s = mixedFractionRe.ReplaceAllString(s, "$1 $2")
	replacer := strings.NewReplacer(
		"½", "1/2", "⅓", "1/3", "⅔", "2/3", "¼", "1/4", "¾", "3/4",
		"⅕", "1/5", "⅖", "2/5", "⅗", "3/5", "⅘", "4/5",
		"⅙", "1/6", "⅚", "5/6", "⅛", "1/8", "⅜", "3/8", "⅝", "5/8", "⅞", "7/8",
		"\u00bc", "1/4", "\u00bd", "1/2", "\u00be", "3/4",
	)
	s = replacer.Replace(s)
	// Collapse ranges like "2-3" or "1-2" to the lower bound (keep first number)
	s = ingredientRangeRe.ReplaceAllString(s, "$1 ")
	return s
}

// splitAttachedUnit splits tokens like "30g" or "1.5kg" into ["30", "g", ...rest].
// BBC Good Food and some other sites write quantity+unit with no space.
func splitAttachedUnit(parts []string) []string {
	if len(parts) == 0 {
		return parts
	}
	token := parts[0]
	// Find where digits/punctuation end and letters begin
	i := 0
	for i < len(token) && (token[i] == '.' || token[i] == '/' || (token[i] >= '0' && token[i] <= '9')) {
		i++
	}
	if i > 0 && i < len(token) {
		// Split "30g" → ["30", "g"]
		return append([]string{token[:i], token[i:]}, parts[1:]...)
	}
	return parts
}

// parseIngredientString does a best-effort parse of strings like "2 cups flour".
// Returns an ImportedIngredient with quantity and unit extracted if recognisable.
func parseIngredientString(s string) ImportedIngredient {
	s = strings.TrimSpace(normalizeIngredientString(s))
	ing := ImportedIngredient{Name: s, Quantity: 1, Unit: "piece"}

	parts := splitAttachedUnit(strings.Fields(s))
	if len(parts) < 2 {
		return ing
	}

	// Try to parse first token as a number (including fractions like "1/2").
	qty, rest := parseQuantity(parts)
	if qty <= 0 {
		return ing
	}
	ing.Quantity = qty

	// Also handle attached unit in the qty token's remainder after splitAttachedUnit
	// re-split in case the rest still has attached unit
	if len(rest) > 0 {
		rest = splitAttachedUnit(rest)
	}

	knownUnits := map[string]string{
		// mass
		"g": "g", "gram": "g", "grams": "g",
		"kg": "kg", "kilogram": "kg", "kilograms": "kg",
		"oz": "oz", "ounce": "oz", "ounces": "oz",
		"lb": "lb", "lbs": "lb", "pound": "lb", "pounds": "lb",
		// volume
		"ml": "ml", "milliliter": "ml", "milliliters": "ml", "millilitre": "ml", "millilitres": "ml",
		"l": "L", "liter": "L", "liters": "L", "litre": "L", "litres": "L",
		"cup": "cup", "cups": "cup", "c": "cup", "c.": "cup",
		"tbsp": "tbsp", "tablespoon": "tbsp", "tablespoons": "tbsp", "tbs": "tbsp", "tbl": "tbsp", "tb": "tbsp",
		"tsp": "tsp", "teaspoon": "tsp", "teaspoons": "tsp", "ts": "tsp",
		// count
		"piece": "piece", "pieces": "piece",
		"slice": "piece", "slices": "piece",
		"whole": "piece",
		"clove": "clove", "cloves": "clove",
		"can": "can", "cans": "can",
		"jar": "jar", "jars": "jar",
		"bunch": "bunch", "bunches": "bunch",
		"sprig": "bunch", "sprigs": "bunch",
		"head": "piece", "heads": "piece",
		"stalk": "piece", "stalks": "piece",
		"stick": "piece", "sticks": "piece",
		"strip": "piece", "strips": "piece",
		"fillet": "piece", "fillets": "piece",
		"breast": "piece", "breasts": "piece",
		"thigh": "piece", "thighs": "piece",
		"leg": "piece", "legs": "piece",
		"sheet": "piece", "sheets": "piece",
		"package": "piece", "pkg": "piece",
		"bag": "piece", "bags": "piece",
		"box": "can", "boxes": "can",
	}

	// Units that need a numeric conversion into a supported unit.
	convertedUnits := map[string]struct {
		unit   string
		factor float64
	}{
		"pt": {"ml", 473.176}, "pint": {"ml", 473.176}, "pints": {"ml", 473.176},
		"qt": {"L", 0.946353}, "quart": {"L", 0.946353}, "quarts": {"L", 0.946353},
		"gal": {"L", 3.78541}, "gallon": {"L", 3.78541}, "gallons": {"L", 3.78541},
		"floz": {"ml", 29.5735}, "fl.oz": {"ml", 29.5735}, "fl.oz.": {"ml", 29.5735},
	}

	if len(rest) == 0 {
		return ing
	}
	unitKey := strings.ToLower(strings.Trim(rest[0], ".,;:()"))
	nameFrom := -1
	if unitKey == "fl" && len(rest) > 1 {
		switch strings.ToLower(strings.Trim(rest[1], ".,;:()")) {
		case "oz", "ounce", "ounces":
			unitKey = "floz"
			nameFrom = 2
		}
	}
	if conv, ok := convertedUnits[unitKey]; ok {
		if nameFrom < 0 {
			nameFrom = 1
		}
		ing.Quantity = math.Round(ing.Quantity*conv.factor*100) / 100
		ing.Unit = conv.unit
		ing.Name = strings.Join(rest[nameFrom:], " ")
		if ing.Name == "" {
			ing.Name = s
		}
		return ing
	}
	if mapped, ok := knownUnits[unitKey]; ok {
		ing.Unit = mapped
		ing.Name = strings.Join(rest[1:], " ")
		if ing.Name == "" {
			ing.Name = s
		}
	} else {
		ing.Unit = "piece"
		ing.Name = strings.Join(rest, " ")
	}
	return ing
}

// parseQuantity tries to read a numeric quantity from the start of parts.
// Returns the quantity and the remaining tokens.
func parseQuantity(parts []string) (float64, []string) {
	if len(parts) == 0 {
		return 0, parts
	}
	token := parts[0]

	// Bare fraction like "1/2" (must precede the float scan, which would read just "1")
	if idx := strings.Index(token, "/"); idx > 0 {
		num, err1 := strconv.ParseFloat(token[:idx], 64)
		den, err2 := strconv.ParseFloat(token[idx+1:], 64)
		if err1 == nil && err2 == nil && den != 0 {
			return num / den, parts[1:]
		}
	}

	// Simple float
	var q float64
	if _, err := fmt.Sscanf(token, "%f", &q); err == nil {
		// Check for a following fraction token like "1/2" (mixed number split by space)
		if len(parts) > 1 {
			if idx := strings.Index(parts[1], "/"); idx > 0 {
				var num, den float64
				if _, err := fmt.Sscanf(parts[1][:idx], "%f", &num); err == nil {
					if _, err := fmt.Sscanf(parts[1][idx+1:], "%f", &den); err == nil && den != 0 {
						return q + num/den, parts[2:]
					}
				}
			}
		}
		return q, parts[1:]
	}

	// Fraction like "1/2" or mixed attached "1½" already normalized to "11/2" — handle "1/2"
	if idx := strings.Index(token, "/"); idx > 0 {
		var num, den float64
		if _, err := fmt.Sscanf(token[:idx], "%f", &num); err == nil {
			if _, err := fmt.Sscanf(token[idx+1:], "%f", &den); err == nil && den != 0 {
				return num / den, parts[1:]
			}
		}
	}

	return 0, parts
}

// stripHTMLString strips HTML tags from a string (for description/instruction fields).
func stripHTMLString(s string) string {
	return stripHTML(strings.NewReader(s))
}

// stripHTML walks HTML tokens and returns visible text, skipping script/style/noscript.
func stripHTML(r io.Reader) string {
	tokenizer := xhtml.NewTokenizer(r)
	var sb strings.Builder
	skipDepth := 0
	skipTags := map[string]bool{"script": true, "style": true, "noscript": true}

	for {
		tt := tokenizer.Next()
		switch tt {
		case xhtml.ErrorToken:
			goto done
		case xhtml.StartTagToken, xhtml.SelfClosingTagToken:
			name, _ := tokenizer.TagName()
			if skipTags[string(name)] {
				skipDepth++
			}
		case xhtml.EndTagToken:
			name, _ := tokenizer.TagName()
			if skipTags[string(name)] && skipDepth > 0 {
				skipDepth--
			}
		case xhtml.TextToken:
			if skipDepth == 0 {
				chunk := strings.TrimFunc(string(tokenizer.Text()), unicode.IsSpace)
				if chunk != "" {
					sb.WriteString(chunk)
					sb.WriteByte('\n')
				}
			}
		}
	}
done:
	return sb.String()
}

// extractPDFText extracts plain text from a PDF file using pdfcpu.
func extractPDFText(path string) (string, error) {
	tmpDir, err := os.MkdirTemp("", "pdftext-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmpDir)

	conf := model.NewDefaultConfiguration()
	conf.ValidationMode = model.ValidationRelaxed
	if err := api.ExtractContentFile(path, tmpDir, nil, conf); err != nil {
		return "", err
	}

	var sb strings.Builder
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(tmpDir, e.Name()))
		if err != nil {
			continue
		}
		sb.Write(data)
		sb.WriteByte('\n')
	}
	return sb.String(), nil
}
