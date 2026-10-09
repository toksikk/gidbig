package wttrin

import (
	"context"
	"errors"
	"testing"
)

func TestStatsCountsLookups(t *testing.T) {
	m := newTestModule()
	fail := false
	m.getWeatherFn = func(string) (wttrinResponse, error) {
		if fail {
			return wttrinResponse{}, errors.New("down")
		}
		return minimalWeatherResponse(), nil
	}
	_, _ = m.getWeatherCached("Berlin")
	_, _ = m.getWeatherCached("berlin")
	fail = true
	_, _ = m.getWeatherCached("Paris")

	st, err := m.Stats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, s := range append(st.Summary, st.Detail...) {
		got[s.Name] = s.Value
	}
	want := map[string]string{"lookups": "3", "cache": "1", "cache hits": "1", "fetch errors": "1", "in flight": "0"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}
