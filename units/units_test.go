package units_test

import (
	"testing"

	"kitchen_manager/units"
)

func TestIsValid(t *testing.T) {
	valid := []string{"g", "kg", "oz", "lb", "ml", "L", "cup", "tbsp", "tsp", "piece", "clove", "can", "jar", "bunch"}
	for _, u := range valid {
		if !units.IsValid(units.Unit(u)) {
			t.Errorf("expected %q to be valid", u)
		}
	}
	if units.IsValid(units.Unit("foobar")) {
		t.Error("expected \"foobar\" to be invalid")
	}
	if units.IsValid(units.Unit("")) {
		t.Error("expected empty string to be invalid")
	}
}

func TestBaseDimension(t *testing.T) {
	cases := map[string]string{
		"g": "mass", "kg": "mass", "oz": "mass", "lb": "mass",
		"ml": "volume", "L": "volume", "cup": "volume", "tbsp": "volume", "tsp": "volume",
		"piece": "count", "clove": "count", "can": "count", "jar": "count", "bunch": "count",
	}
	for u, want := range cases {
		got := units.BaseDimension(units.Unit(u))
		if got != want {
			t.Errorf("BaseDimension(%q) = %q, want %q", u, got, want)
		}
	}
}

func TestConvert_SameDimension(t *testing.T) {
	cases := []struct {
		qty        float64
		from, to   units.Unit
		wantApprox float64
	}{
		{1000, "g", "kg", 1.0},
		{1, "kg", "g", 1000.0},
		{1, "L", "ml", 1000.0},
		{1000, "ml", "L", 1.0},
		{1, "cup", "ml", 236.588},
		{3, "tbsp", "tsp", 9.0},
		{16, "oz", "lb", 1.0},
		{1, "lb", "oz", 16.0},
	}
	for _, c := range cases {
		got, err := units.Convert(c.qty, c.from, c.to)
		if err != nil {
			t.Errorf("Convert(%v, %q, %q) unexpected error: %v", c.qty, c.from, c.to, err)
			continue
		}
		diff := got - c.wantApprox
		if diff < -0.01 || diff > 0.01 {
			t.Errorf("Convert(%v, %q, %q) = %v, want ~%v", c.qty, c.from, c.to, got, c.wantApprox)
		}
	}
}

func TestConvert_CrossDimensionError(t *testing.T) {
	_, err := units.Convert(1, "g", "ml")
	if err == nil {
		t.Error("expected error for cross-dimension conversion g→ml")
	}
	_, err = units.Convert(1, "piece", "g")
	if err == nil {
		t.Error("expected error for cross-dimension conversion piece→g")
	}
}

func TestConvert_SameUnit(t *testing.T) {
	got, err := units.Convert(42.5, "g", "g")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 42.5 {
		t.Errorf("Convert(42.5, g, g) = %v, want 42.5", got)
	}
}

func TestBaseUnit(t *testing.T) {
	if units.BaseUnit("kg") != "g" {
		t.Error("BaseUnit(kg) should be g")
	}
	if units.BaseUnit("L") != "ml" {
		t.Error("BaseUnit(L) should be ml")
	}
	if units.BaseUnit("piece") != "piece" {
		t.Error("BaseUnit(piece) should be piece (count units unchanged)")
	}
}
