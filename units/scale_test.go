package units

import "testing"

func TestServingsScale(t *testing.T) {
	for _, c := range []struct {
		req, base int
		want      float64
	}{{4, 2, 2}, {1, 4, 0.25}, {3, 3, 1}, {2, 0, 2}, {0, 4, 1}, {-1, 4, 1}} {
		if got := ServingsScale(c.req, c.base); got != c.want {
			t.Errorf("(%d,%d)=%v want %v", c.req, c.base, got, c.want)
		}
	}
	if got := ScaleQuantity(100, 3, 2); got != 150 {
		t.Errorf("got %v", got)
	}
}
