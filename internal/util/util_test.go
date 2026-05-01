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

func TestRandomRange_panicOnEqualMinMax(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for RandomRange(5,5), got none")
		}
	}()
	RandomRange(5, 5)
}

func TestRandomRange_panicOnMaxLessThanMin(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for RandomRange(7,3), got none")
		}
	}()
	RandomRange(7, 3)
}

func TestRandomRange_panicOnZeroRange(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for RandomRange(0,0), got none")
		}
	}()
	RandomRange(0, 0)
}
