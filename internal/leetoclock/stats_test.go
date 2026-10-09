package leetoclock

import (
	"context"
	"testing"
)

func TestStatsWithoutStore(t *testing.T) {
	if _, err := New().Stats(context.Background()); err == nil {
		t.Fatal("expected error without store")
	}
}
