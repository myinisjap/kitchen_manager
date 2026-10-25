package units

// ServingsScale returns the multiplier for a recipe written for base servings
// when making requested servings. base<=0 is treated as 1; requested<=0 means as written.
func ServingsScale(requested, base int) float64 {
	if base <= 0 {
		base = 1
	}
	if requested <= 0 {
		return 1
	}
	return float64(requested) / float64(base)
}

// ScaleQuantity scales qty by ServingsScale.
func ScaleQuantity(qty float64, requested, base int) float64 {
	return qty * ServingsScale(requested, base)
}
