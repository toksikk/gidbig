package openholidays

import (
	"context"
	"testing"
)

func TestStats(t *testing.T) {
	m, _ := newTestModule(t)
	ctx := context.Background()
	q := query{country: "DE", kind: typePublic, from: "2026-10-25", to: "2027-10-24"}
	_, _ = m.lookup(ctx, q)
	_, _ = m.lookup(ctx, q)
	_, _ = m.getCountries(ctx)
	st, err := m.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, s := range append(st.Summary, st.Detail...) {
		got[s.Name] = s.Value
	}
	want := map[string]string{"holiday lookups": "2", "cache": "1", "cache hits": "1", "cache misses": "1", "metadata lookups": "1", "api errors": "0", "in flight": "0"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}
