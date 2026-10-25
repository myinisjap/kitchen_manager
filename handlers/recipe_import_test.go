package handlers

import (
	"math"
	"strings"
	"testing"
)

func TestParseIngredientUnitConversion(t *testing.T) {
	cases := []struct {
		in   string
		qty  float64
		unit string
		name string
	}{
		{"1 pint milk", 473.18, "ml", "milk"},
		{"2 quarts stock", 1.89, "L", "stock"},
		{"1 gallon water", 3.79, "L", "water"},
		{"8 fl oz cream", 236.59, "ml", "cream"},
		{"2 cups flour", 2, "cup", "flour"},
	}
	for _, c := range cases {
		got := parseIngredientString(c.in)
		if math.Abs(got.Quantity-c.qty) > 0.02 || got.Unit != c.unit || got.Name != c.name {
			t.Errorf("%q -> %+v", c.in, got)
		}
	}
}

func TestParseIngredientStringCases(t *testing.T) {
	cases := []struct {
		in   string
		qty  float64
		unit string
		name string
	}{
		{"2 cups flour", 2, "cup", "flour"},
		{"1 1/2 cups milk", 1.5, "cup", "milk"},
		{"1/2 cup sugar", 0.5, "cup", "sugar"},
		{"½ tsp salt", 0.5, "tsp", "salt"},
		{"1½ cups rice", 1.5, "cup", "rice"},
		{"30g butter", 30, "g", "butter"},
		{"1.5kg potatoes", 1.5, "kg", "potatoes"},
		{"2-3 cloves garlic", 2, "clove", "garlic"},
		{"3 eggs", 3, "piece", "eggs"},
		{"salt", 1, "piece", "salt"},
		{"pinch of salt", 1, "piece", "pinch of salt"},
		{"2 Tbsp. oil", 2, "tbsp", "oil"},
		{"1 can tomatoes", 1, "can", "tomatoes"},
		{"2 lbs beef", 2, "lb", "beef"},
		{"1 fl. oz vanilla", 29.57, "ml", "vanilla"},
		{"1 fl oz vanilla", 29.57, "ml", "vanilla"},
		{"2 cups", 2, "cup", "2 cups"},
	}
	for _, c := range cases {
		got := parseIngredientString(c.in)
		if math.Abs(got.Quantity-c.qty) > 0.02 || got.Unit != c.unit || got.Name != c.name {
			t.Errorf("%q -> %+v", c.in, got)
		}
	}
}

func TestParseIngredientBareFraction(t *testing.T) {
	for _, in := range []string{"1/2 cup sugar", "½ tsp salt"} {
		got := parseIngredientString(in)
		if got.Quantity != 0.5 {
			t.Errorf("%q: quantity %v, want 0.5", in, got.Quantity)
		}
	}
}

func TestParseQuantity(t *testing.T) {
	q, rest := parseQuantity([]string{"2", "cups"})
	if q != 2 || len(rest) != 1 || rest[0] != "cups" {
		t.Errorf("got %v %v", q, rest)
	}
	if q, _ := parseQuantity([]string{"1", "1/2", "cups"}); q != 1.5 {
		t.Errorf("mixed: %v", q)
	}
	if q, rest := parseQuantity([]string{"abc", "x"}); q != 0 || len(rest) != 2 {
		t.Errorf("abc: %v %v", q, rest)
	}
	if q, rest := parseQuantity(nil); q != 0 || len(rest) != 0 {
		t.Errorf("empty: %v %v", q, rest)
	}
}

func TestNormalizeIngredientString(t *testing.T) {
	if got := normalizeIngredientString("½ cup"); got != "1/2 cup" {
		t.Errorf("got %q", got)
	}
	if got := normalizeIngredientString("2-3 cups"); !strings.HasPrefix(got, "2 ") {
		t.Errorf("got %q", got)
	}
}

func TestSplitAttachedUnit(t *testing.T) {
	got := splitAttachedUnit([]string{"30g", "butter"})
	if len(got) != 3 || got[0] != "30" || got[1] != "g" || got[2] != "butter" {
		t.Errorf("got %v", got)
	}
	for _, in := range [][]string{{"g"}, {"30"}, {}} {
		if out := splitAttachedUnit(in); len(out) != len(in) {
			t.Errorf("%v -> %v", in, out)
		}
	}
}

func TestExtractJSONLD(t *testing.T) {
	html := `<html><head><script type="application/ld+json">
{"@context":"https://schema.org","@graph":[{"@type":"WebSite"},{"@type":"Recipe","name":"Pancakes","recipeYield":"4","recipeIngredient":["2 cups flour","3 eggs"]}]}
</script></head></html>`
	r := extractJSONLD([]byte(html))
	if r == nil || r.Name != "Pancakes" || len(r.Ingredients) != 2 {
		t.Fatalf("got %+v", r)
	}
	if r.Ingredients[0].Name != "flour" {
		t.Errorf("ingredients %+v", r.Ingredients)
	}
	if extractJSONLD([]byte("<html></html>")) != nil {
		t.Error("expected nil without JSON-LD")
	}
}
