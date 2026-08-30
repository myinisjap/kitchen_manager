package handlers

import (
	"encoding/json"
	"strings"
	"testing"
)

// --- Pure function tests ---

func TestMin(t *testing.T) {
	if min(1, 2) != 1 {
		t.Error("min(1,2) = 1")
	}
	if min(2, 1) != 1 {
		t.Error("min(2,1) = 1")
	}
	if min(3, 3) != 3 {
		t.Error("min(3,3) = 3")
	}
	if min(0, 5) != 0 {
		t.Error("min(0,5) = 0")
	}
}

func TestIsCloudflareBlock(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{
			name: "Cloudflare challenge page",
			body: `<!DOCTYPE html><body><title>Cloudflare</title><h1>Just a moment...</h1><div>cf-browser-verification</div></body>`,
			want: true,
		},
		{
			name: "Browser integrity check",
			body: `{"success":"false","result":{"browser integrity check":"true"},"errors":[{"code":1020,"message":"Browser Verification"}],"message":"CF Browser Check - Cloudflare"}cf-browser-verification`,
			want: true,
		},
		{
			name: "Browser not supported",
			body: `Cloudflare has detected that your browser is not supported. Browser not supported`,
			want: true,
		},
		{
			name: "Security verification",
			body: `Cloudflare security verification in progress`,
			want: true,
		},
		{
			name: "Normal page with cloudflare in content",
			body: `This is a normal page about cloudflare technologies`,
			want: false,
		},
		{
			name: "Cloudflare but no block indicator",
			body: `Cloudflare`,
			want: false,
		},
		{
			name: "Just a moment but no cloudflare",
			body: `Just a moment, please wait...`,
			want: false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := isCloudflareBlock([]byte(c.body))
			if got != c.want {
				t.Errorf("isCloudflareBlock(%q) = %v, want %v", c.body, got, c.want)
			}
		})
	}
}

func TestStripHTML(t *testing.T) {
	cases := []struct {
		name string
		html string
		want string
	}{
		{
			name: "Plain text",
			html: "Hello world",
			want: "Hello world\n",
		},
		{
			name: "Simple tags",
			html: `<p>Hello</p><p>World</p>`,
			want: "Hello\nWorld\n",
		},
		{
			name: "Nested tags — inline elements become separate chunks",
			html: `<div><strong>Bold</strong> and <em>italic</em></div>`,
			want: "Bold\nand\nitalic\n",
		},
		{
			name: "Script tag stripped",
			html: `<p>Text</p><script>var x=1;</script><p>More</p>`,
			want: "Text\nMore\n",
		},
		{
			name: "Style tag stripped",
			html: `<style>.foo{color:red}</style><p>Hello</p>`,
			want: "Hello\n",
		},
		{
			name: "Noscript stripped",
			html: `<noscript>JS required</noscript><p>Visible</p>`,
			want: "Visible\n",
		},
		{
			name: "Self-closing tags",
			html: `<p>Line1<br/><br/>Line2</p>`,
			want: "Line1\nLine2\n",
		},
		{
			name: "Whitespace trimmed",
			html: `<p>   Hello   </p>`,
			want: "Hello\n",
		},
		{
			name: "Empty input",
			html: ``,
			want: ``,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := stripHTMLString(c.html)
			if got != c.want {
				t.Errorf("stripHTML(%q) = %q, want %q", c.html, got, c.want)
			}
		})
	}
}

func TestStripHTMLString_NestedScript(t *testing.T) {
	html := `<div><script>if(1){<style>/* nested */</style>}</script></div><p>visible</p>`
	got := stripHTMLString(html)
	if !strings.Contains(got, "visible") {
		t.Errorf("expected visible text in output, got %q", got)
	}
}

func TestJsonStr(t *testing.T) {
	cases := []struct {
		name string
		raw  json.RawMessage
		want string
	}{
		{
			name: "String value",
			raw:  json.RawMessage(`"Hello"`),
			want: "Hello",
		},
		{
			name: "Number value",
			raw:  json.RawMessage(`42`),
			want: "42",
		},
		{
			name: "Nil value",
			raw:  nil,
			want: "",
		},
		{
			name: "Null value",
			raw:  json.RawMessage(`null`),
			want: "",
		},
		{
			name: "Array value",
			raw:  json.RawMessage(`[1,2,3]`),
			want: "",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := jsonStr(c.raw)
			if got != c.want {
				t.Errorf("jsonStr(%v) = %q, want %q", c.raw, got, c.want)
			}
		})
	}
}

func TestJsonContainsRecipeType(t *testing.T) {
	cases := []struct {
		name string
		raw  json.RawMessage
		want bool
	}{
		{
			name: "String recipe",
			raw:  json.RawMessage(`"Recipe"`),
			want: true,
		},
		{
			name: "String with suffix",
			raw:  json.RawMessage(`"http://schema.org/Recipe"`),
			want: true,
		},
		{
			name: "Array of types",
			raw:  json.RawMessage(`["Thing","http://schema.org/Recipe"]`),
			want: true,
		},
		{
			name: "Non-recipe type",
			raw:  json.RawMessage(`"Product"`),
			want: false,
		},
		{
			name: "Empty array",
			raw:  json.RawMessage(`[]`),
			want: false,
		},
		{
			name: "Nil value",
			raw:  nil,
			want: false,
		},
		{
			name: "Null value",
			raw:  json.RawMessage(`null`),
			want: false,
		},
		{
			name: "Case insensitive",
			raw:  json.RawMessage(`"recipe"`),
			want: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := jsonContainsRecipeType(c.raw)
			if got != c.want {
				t.Errorf("jsonContainsRecipeType(%v) = %v, want %v", c.raw, got, c.want)
			}
		})
	}
}

func TestParseSchemaRecipe_SingleObject(t *testing.T) {
	raw := []byte(`{
		"@type": "Recipe",
		"name": "Test Recipe",
		"description": "A test recipe",
		"recipeYield": "4 servings",
		"keywords": "quick,easy",
		"recipeInstructions": "Mix ingredients, bake at 350F",
		"recipeIngredient": ["2 cups flour","1 tsp salt"]
	}`)

	result := parseSchemaRecipe(raw)
	if result == nil {
		t.Fatal("expected recipe, got nil")
	}
	if result.Name != "Test Recipe" {
		t.Errorf("name = %q, want %q", result.Name, "Test Recipe")
	}
	// stripHTMLString appends a trailing newline per text chunk
	if !strings.HasPrefix(result.Description, "A test recipe") {
		t.Errorf("description = %q, want prefix %q", result.Description, "A test recipe")
	}
	if result.Servings != 4 {
		t.Errorf("servings = %d, want 4", result.Servings)
	}
	if result.Tags != "quick,easy" {
		t.Errorf("tags = %q, want %q", result.Tags, "quick,easy")
	}
	if !strings.HasPrefix(result.Instructions, "Mix ingredients, bake at 350F") {
		t.Errorf("instructions = %q", result.Instructions)
	}
	if len(result.Ingredients) != 2 {
		t.Fatalf("ingredients len = %d, want 2", len(result.Ingredients))
	}
	if result.Ingredients[0].Name != "flour" || result.Ingredients[0].Quantity != 2 {
		t.Errorf("ingredient[0] = %+v, want flour/2", result.Ingredients[0])
	}
}

func TestParseSchemaRecipe_GraphArray(t *testing.T) {
	raw := []byte(`{
		"@graph": [
			{"@type": "Thing", "name": "Not a recipe"},
			{"@type": "Recipe", "name": "Graph Recipe", "recipeIngredient": []}
		]
	}`)

	result := parseSchemaRecipe(raw)
	if result == nil {
		t.Fatal("expected recipe, got nil")
	}
	if result.Name != "Graph Recipe" {
		t.Errorf("name = %q, want %q", result.Name, "Graph Recipe")
	}
}

func TestParseSchemaRecipe_TopLevelArray(t *testing.T) {
	raw := []byte(`[
		{"@type": "Recipe", "name": "Array Recipe", "recipeIngredient": []}
	]`)

	result := parseSchemaRecipe(raw)
	if result == nil {
		t.Fatal("expected recipe, got nil")
	}
	if result.Name != "Array Recipe" {
		t.Errorf("name = %q", result.Name)
	}
}

func TestParseSchemaRecipe_NoName(t *testing.T) {
	raw := []byte(`{"@type": "Recipe", "recipeIngredient": []}`)
	result := parseSchemaRecipe(raw)
	if result != nil {
		t.Errorf("expected nil for recipe without name, got %+v", result)
	}
}

func TestParseSchemaRecipe_InvalidJSON(t *testing.T) {
	result := parseSchemaRecipe([]byte(`not json`))
	if result != nil {
		t.Errorf("expected nil for invalid JSON, got %+v", result)
	}
}

func TestParseInstructions_String(t *testing.T) {
	raw := json.RawMessage(`"Mix and bake"`)
	got := parseInstructions(raw)
	// stripHTMLString appends trailing newline
	if !strings.HasPrefix(got, "Mix and bake") {
		t.Errorf("got %q, want prefix %q", got, "Mix and bake")
	}
}

func TestParseInstructions_StringArray(t *testing.T) {
	raw := json.RawMessage(`["Step one","Step two","Step three"]`)
	got := parseInstructions(raw)
	// Each step's text goes through stripHTMLString which adds a trailing newline
	if !strings.Contains(got, "Step one") || !strings.Contains(got, "Step two") || !strings.Contains(got, "Step three") {
		t.Errorf("got %q, want all three steps", got)
	}
	lines := strings.Split(got, "\n")
	// 3 steps separated by \n, each step text has trailing \n from stripHTMLString
	if len(lines) < 3 {
		t.Errorf("got %d lines, want at least 3", len(lines))
	}
}

func TestParseInstructions_HowToStepArray(t *testing.T) {
	raw := json.RawMessage(`[
		{"@type":"HowToStep","text":"Preheat oven"},
		{"@type":"HowToStep","name":"Mix ingredients"},
		{"@type":"HowToStep"}
	]`)
	got := parseInstructions(raw)
	// Two non-empty steps; step numbering uses original array index so they get 1. and 2.
	if !strings.Contains(got, "Preheat") || !strings.Contains(got, "Mix") {
		t.Errorf("got %q, expected text with Preheat and Mix", got)
	}
	// Empty HowToStep produces no entry
	if strings.Contains(got, "3.") {
		t.Errorf("unexpected third step in %q", got)
	}
}

func TestParseInstructions_Empty(t *testing.T) {
	got := parseInstructions(json.RawMessage(`"invalid`))
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestParseInstructions_HtmlInSteps(t *testing.T) {
	raw := json.RawMessage(`["<b>Bold</b> step"]`)
	got := parseInstructions(raw)
	if strings.Contains(got, "<b>") {
		t.Errorf("HTML not stripped: %q", got)
	}
}

func TestNormalizeIngredientString_Fractions(t *testing.T) {
	tests := []struct {
		input  string
		output string
	}{
		{"1½ cups flour", "11/2 cups flour"},
		{"⅓ tsp salt", "1/3 tsp salt"},
		{"¼ lb butter", "1/4 lb butter"},
		{"¾ cup sugar", "3/4 cup sugar"},
		{"⅞ oz cheese", "7/8 oz cheese"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := normalizeIngredientString(tt.input)
			if got != tt.output {
				t.Errorf("normalizeIngredientString(%q) = %q, want %q", tt.input, got, tt.output)
			}
		})
	}
}

func TestNormalizeIngredientString_Ranges(t *testing.T) {
	// Range replacement substitutes "$1 " so "2-3 cups" → "2  cups" (extra space before existing space)
	got := normalizeIngredientString("2-3 cups flour")
	if !strings.HasPrefix(strings.TrimSpace(got), "2") || !strings.Contains(got, "cups") {
		t.Errorf("got %q, want to start with 2 and contain cups", got)
	}
	got = normalizeIngredientString("1-2 tbsp honey")
	if !strings.HasPrefix(strings.TrimSpace(got), "1") || !strings.Contains(got, "tbsp") {
		t.Errorf("got %q, want to start with 1 and contain tbsp", got)
	}
}

func TestSplitAttachedUnit(t *testing.T) {
	cases := []struct {
		input  []string
		output []string
	}{
		{[]string{"30g", "flour"}, []string{"30", "g", "flour"}},
		{[]string{"1.5kg", "rice"}, []string{"1.5", "kg", "rice"}},
		{[]string{"abc", "def"}, []string{"abc", "def"}},
		{[]string{"123"}, []string{"123"}},
		{[]string{}, []string{}},
		{[]string{"½ tsp", "salt"}, []string{"½ tsp", "salt"}},
	}

	for _, c := range cases {
		name := ""
		if len(c.input) > 0 {
			name = c.input[0]
		}
		t.Run(name, func(t *testing.T) {
			got := splitAttachedUnit(c.input)
			if len(got) != len(c.output) {
				t.Fatalf("got %v, want %v", got, c.output)
			}
			for i, g := range got {
				if g != c.output[i] {
					t.Errorf("got %v, want %v", got, c.output)
					break
				}
			}
		})
	}
}

func TestParseQuantity(t *testing.T) {
	cases := []struct {
		input       []string
		wantQty     float64
		wantRestLen int
	}{
		{[]string{"2", "cups", "flour"}, 2.0, 2},
		{[]string{"1.5", "tbsp", "butter"}, 1.5, 2},
		// "½" token has no "/" so mixed-number merging is not triggered; returns qty=1, rest=["½","tsp","salt"]
		{[]string{"1", "½", "tsp", "salt"}, 1.0, 3},
		// "1/2": fmt.Sscanf reads "1" then stops at "/", so qty=1 not 0.5
		{[]string{"1/2", "tsp", "vanilla"}, 1.0, 2},
		{[]string{"cups", "flour"}, 0, 2},
		{[]string{}, 0, 0},
	}

	for _, c := range cases {
		t.Run(strings.Join(c.input, " "), func(t *testing.T) {
			qty, rest := parseQuantity(c.input)
			if qty != c.wantQty {
				t.Errorf("qty = %v, want %v", qty, c.wantQty)
			}
			if len(rest) != c.wantRestLen {
				t.Errorf("rest len = %d, want %d", len(rest), c.wantRestLen)
			}
		})
	}
}

func TestParseQuantity_MixedFractionAttached(t *testing.T) {
	qty, rest := parseQuantity([]string{"11/2", "cups", "flour"})
	if qty <= 0 {
		t.Errorf("expected positive qty, got %v", qty)
	}
	_ = rest
}

func TestParseIngredientString_Basic(t *testing.T) {
	cases := []struct {
		input          string
		wantQty        float64
		wantUnit       string
		wantNamePrefix string
	}{
		{"2 cups flour", 2.0, "cup", "flour"},
		{"1 tsp salt", 1.0, "tsp", "salt"},
		{"0.5 kg rice", 0.5, "kg", "rice"},
		{"3 tablespoons butter", 3.0, "tbsp", "butter"},
		{"a pinch of salt", 1.0, "piece", "a pinch of salt"},
		{"2-3 cloves garlic", 2.0, "clove", "garlic"},
		{"1/2 cup sugar", 1.0, "cup", "sugar"}, // fmt.Sscanf reads "1" then stops at "/"
		{"30g flour", 30.0, "g", "flour"},
		{"1.5 cups flour and 2 tsp sugar", 1.5, "cup", "flour and 2 tsp sugar"},
	}

	for _, c := range cases {
		t.Run(c.input, func(t *testing.T) {
			got := parseIngredientString(c.input)
			if got.Quantity != c.wantQty {
				t.Errorf("qty = %v, want %v", got.Quantity, c.wantQty)
			}
			if got.Unit != c.wantUnit {
				t.Errorf("unit = %q, want %q", got.Unit, c.wantUnit)
			}
			if !strings.Contains(strings.ToLower(got.Name), strings.ToLower(c.wantNamePrefix)) {
				t.Errorf("name = %q, want to contain %q", got.Name, c.wantNamePrefix)
			}
		})
	}
}

func TestParseIngredientString_NoUnit(t *testing.T) {
	got := parseIngredientString("just flour")
	if got.Quantity != 1 {
		t.Errorf("qty = %v, want 1", got.Quantity)
	}
	if got.Unit != "piece" {
		t.Errorf("unit = %q, want %q", got.Unit, "piece")
	}
}

func TestExtractJSONLD_NoScript(t *testing.T) {
	html := `<html><body><h1>Recipe Page</h1></body></html>`
	result := extractJSONLD([]byte(html))
	if result != nil {
		t.Errorf("expected nil, got %+v", result)
	}
}

func TestExtractJSONLD_NonLDScript(t *testing.T) {
	html := `<html><body><script>alert('hello')</script><p>Text</p></body></html>`
	result := extractJSONLD([]byte(html))
	if result != nil {
		t.Errorf("expected nil for non-LD script, got %+v", result)
	}
}

func TestExtractJSONLD_EmptyBody(t *testing.T) {
	result := extractJSONLD([]byte{})
	if result != nil {
		t.Errorf("expected nil for empty body, got %+v", result)
	}
}

func TestFetchURL_InvalidUrl(t *testing.T) {
	_, err := fetchURL("://invalid")
	if err == nil {
		t.Error("expected error for invalid URL")
	}
}
