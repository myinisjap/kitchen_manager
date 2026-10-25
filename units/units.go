package units

import (
	"fmt"
	"strings"
)

// Unit is a validated unit of measure.
type Unit string

const (
	Gram       Unit = "g"
	Kilogram   Unit = "kg"
	Ounce      Unit = "oz"
	Pound      Unit = "lb"
	Milliliter Unit = "ml"
	Liter      Unit = "L"
	Cup        Unit = "cup"
	Tablespoon Unit = "tbsp"
	Teaspoon   Unit = "tsp"
	Piece      Unit = "piece"
	Clove      Unit = "clove"
	Can        Unit = "can"
	Jar        Unit = "jar"
	Bunch      Unit = "bunch"
)

// toBaseML converts a volume quantity to milliliters.
var toBaseML = map[Unit]float64{
	Milliliter: 1,
	Liter:      1000,
	Cup:        236.588,
	Tablespoon: 14.787,
	Teaspoon:   4.929,
}

// toBaseG converts a mass quantity to grams.
var toBaseG = map[Unit]float64{
	Gram:     1,
	Kilogram: 1000,
	Ounce:    28.3495,
	Pound:    453.592,
}

var dimensions = map[Unit]string{
	Gram: "mass", Kilogram: "mass", Ounce: "mass", Pound: "mass",
	Milliliter: "volume", Liter: "volume", Cup: "volume", Tablespoon: "volume", Teaspoon: "volume",
	Piece: "count", Clove: "count", Can: "count", Jar: "count", Bunch: "count",
}

// IsValid reports whether u is a known unit.
func IsValid(u Unit) bool {
	_, ok := dimensions[u]
	return ok
}

// BaseDimension returns "mass", "volume", or "count" for a valid unit.
// Returns "" for unknown units.
func BaseDimension(u Unit) string {
	return dimensions[u]
}

// BaseUnit returns the base unit for a dimension: "g" for mass, "ml" for volume,
// unchanged for count units.
func BaseUnit(u Unit) Unit {
	switch dimensions[u] {
	case "mass":
		return Gram
	case "volume":
		return Milliliter
	default:
		return u
	}
}

// Convert converts qty from unit `from` to unit `to`.
// Returns an error if the units are in different dimensions.
func Convert(qty float64, from, to Unit) (float64, error) {
	if from == to {
		return qty, nil
	}
	dimFrom := dimensions[from]
	dimTo := dimensions[to]
	if dimFrom == "" || dimTo == "" {
		return 0, fmt.Errorf("unknown unit: %q or %q", from, to)
	}
	if dimFrom != dimTo {
		return 0, fmt.Errorf("cannot convert %q (%s) to %q (%s): different dimensions", from, dimFrom, to, dimTo)
	}
	switch dimFrom {
	case "mass":
		base := qty * toBaseG[from]
		return base / toBaseG[to], nil
	case "volume":
		base := qty * toBaseML[from]
		return base / toBaseML[to], nil
	default:
		// count: no conversion between different count units
		return 0, fmt.Errorf("cannot convert between count units %q and %q", from, to)
	}
}

// all lists every unit in display order (mass, volume, count). Add new units
// here and in dimensions.
var all = []Unit{Gram, Kilogram, Ounce, Pound, Milliliter, Liter, Cup, Tablespoon, Teaspoon, Piece, Clove, Can, Jar, Bunch}

// All returns every unit in display order.
func All() []Unit {
	return append([]Unit(nil), all...)
}

// ByDimension groups unit symbols by dimension ("mass", "volume", "count").
func ByDimension() map[string][]string {
	out := map[string][]string{}
	for _, u := range all {
		d := dimensions[u]
		out[d] = append(out[d], string(u))
	}
	return out
}

// ValidList is the comma-separated unit list used in error messages.
func ValidList() string {
	s := make([]string, len(all))
	for i, u := range all {
		s[i] = string(u)
	}
	return strings.Join(s, ", ")
}
