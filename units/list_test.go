package units

import (
	"reflect"
	"testing"
)

func TestValidList(t *testing.T) {
	want := "g, kg, oz, lb, ml, L, cup, tbsp, tsp, piece, clove, can, jar, bunch"
	if got := ValidList(); got != want {
		t.Errorf("got %q", got)
	}
}

func TestByDimension(t *testing.T) {
	want := map[string][]string{
		"mass":   {"g", "kg", "oz", "lb"},
		"volume": {"ml", "L", "cup", "tbsp", "tsp"},
		"count":  {"piece", "clove", "can", "jar", "bunch"},
	}
	if got := ByDimension(); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v", got)
	}
}

func TestAllMatchesDimensions(t *testing.T) {
	if len(all) != len(dimensions) {
		t.Errorf("all has %d, dimensions %d", len(all), len(dimensions))
	}
	for _, u := range all {
		if _, ok := dimensions[u]; !ok {
			t.Errorf("%q missing from dimensions", u)
		}
	}
	if len(All()) != len(all) {
		t.Error("All length")
	}
}
