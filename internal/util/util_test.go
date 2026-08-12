package util

import (
	"testing"
	"time"
)

func TestRandomRange_normal(t *testing.T) {
	for i := 0; i < 100; i++ {
		v := RandomRange(0, 10)
		if v < 0 || v >= 10 {
			t.Errorf("RandomRange(0,10) = %d, want [0,10)", v)
		}
	}
}

func TestRandomRange_equalMinMax(t *testing.T) {
	if v := RandomRange(5, 5); v != 5 {
		t.Errorf("RandomRange(5,5) = %d, want 5", v)
	}
}

func TestRandomRange_maxLessThanMin(t *testing.T) {
	if v := RandomRange(7, 3); v != 7 {
		t.Errorf("RandomRange(7,3) = %d, want 7", v)
	}
}

func TestRandomRange_zeroRange(t *testing.T) {
	if v := RandomRange(0, 0); v != 0 {
		t.Errorf("RandomRange(0,0) = %d, want 0", v)
	}
}

func TestIsHalloween(t *testing.T) {
	if !isHalloween(time.Date(2026, time.October, 31, 12, 0, 0, 0, time.UTC)) {
		t.Fatal("October 31 should be Halloween")
	}
	if isHalloween(time.Date(2026, time.October, 30, 12, 0, 0, 0, time.UTC)) {
		t.Fatal("October 30 should not be Halloween")
	}
}
