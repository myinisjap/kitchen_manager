package handlers

import (
	"math"
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
