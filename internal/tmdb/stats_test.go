package tmdb

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/toksikk/gidbig/internal/bot"
	"github.com/toksikk/gidbig/internal/cfg"
	"github.com/toksikk/gidbig/internal/tool"
)

var _ bot.StatsProvider = (*Module)(nil)

func statValue(t *testing.T, s bot.ModuleStats, name string) string {
	t.Helper()
	for _, st := range append(append([]bot.Stat{}, s.Summary...), s.Detail...) {
		if st.Name == name {
			return st.Value
		}
	}
	t.Fatalf("stat %q missing in %+v", name, s)
	return ""
}

func TestStats_CountsLookupsCacheAndErrors(t *testing.T) {
	var status atomic.Int32
	status.Store(http.StatusOK)
	srv := newStub(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(int(status.Load()))
		if strings.HasPrefix(r.URL.Path, "/search/") {
			_, _ = w.Write([]byte(multiJSON))
			return
		}
		_, _ = w.Write([]byte(detailsJSON))
	})
	m := testModule(srv.URL)
	ctx := context.Background()

	m.searchReply(ctx, "Dark", "1", "DE")
	if _, err := m.streamingToolHandler(ctx, tool.Env{}, json.RawMessage(`{"title":"Dark"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := m.details(ctx, kindTV, 70523); err != nil { // cache hit after the tool call
		t.Fatal(err)
	}
	status.Store(http.StatusTooManyRequests)
	m.searchReply(ctx, "Dark", "1", "DE")
	status.Store(http.StatusInternalServerError)
	if _, err := m.details(ctx, kindMovie, 2); err == nil {
		t.Fatal("details = nil error on 500")
	}

	s, err := m.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"searches": "2", "picks": "0", "tool calls": "1", "cache hits": "1",
		"cached titles": "1", "errors": "2", "rate limited": "1",
	} {
		if got := statValue(t, s, name); got != want {
			t.Errorf("%s = %s, want %s", name, got, want)
		}
	}
}

func TestStats_RegisteredWithBot(t *testing.T) {
	reg := bot.NewStatsRegistry()
	conf := &cfg.Config{}
	conf.TMDB.ReadAccessToken = testToken
	b := bot.New(bot.Deps{Config: conf, Stats: reg})
	if err := b.RegisterModule(New()); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range reg.All() {
		if p.Name() == "tmdb" {
			found = true
		}
	}
	if !found {
		t.Error("tmdb not in stats registry")
	}
}
