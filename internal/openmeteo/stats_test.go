package openmeteo

import (
	"context"
	"testing"
)

func TestStats(t *testing.T) {
	m, _ := newTestModule(t)
	ctx := context.Background()
	_, _, _ = m.lookup(ctx, "Winterberg", false, 0, 0, 7)
	_, _, _ = m.lookup(ctx, "Winterberg", false, 0, 0, 7)
	st, err := m.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, s := range append(st.Summary, st.Detail...) {
		got[s.Name] = s.Value
	}
	want := map[string]string{"forecast lookups": "2", "geocode lookups": "2", "cache": "1", "cache hits": "1", "geocode hits": "1", "fetch errors": "0", "in flight": "0"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}
