package util

import "testing"

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
