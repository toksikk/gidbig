package openmeteo

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
)

func TestCacheHitAndExpiry(t *testing.T) {
	m, f := newTestModule(t)
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	ctx := context.Background()
	for range 2 {
		if _, _, err := m.lookup(ctx, "Winterberg", false, 0, 0, 7); err != nil {
			t.Fatal(err)
		}
	}
	if f.forecastCalls.Load() != 1 || f.geocodeCalls.Load() != 1 {
		t.Fatalf("forecast=%d geocode=%d, want 1/1", f.forecastCalls.Load(), f.geocodeCalls.Load())
	}
	now = now.Add(forecastCacheTTL + time.Second)
	if _, _, err := m.lookup(ctx, "Winterberg", false, 0, 0, 7); err != nil {
		t.Fatal(err)
	}
	if f.forecastCalls.Load() != 2 || f.geocodeCalls.Load() != 1 {
		t.Fatalf("after TTL forecast=%d geocode=%d, want 2/1", f.forecastCalls.Load(), f.geocodeCalls.Load())
	}
}

func TestCacheKeyRoundsCoords(t *testing.T) {
	m, f := newTestModule(t)
	ctx := context.Background()
	_, _, _ = m.lookup(ctx, "a", true, 51.191, 8.531, 7)
	_, _, _ = m.lookup(ctx, "b", true, 51.192, 8.532, 7)
	_, _, _ = m.lookup(ctx, "b", true, 51.192, 8.532, 3)
	if got := f.forecastCalls.Load(); got != 2 {
		t.Fatalf("forecast calls = %d, want 2", got)
	}
}

func TestSingleFlight(t *testing.T) {
	m, f := newTestModule(t)
	f.delay = 50 * time.Millisecond
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			if _, _, err := m.lookup(context.Background(), "x", true, 1, 1, 7); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if got := f.forecastCalls.Load(); got != 1 {
		t.Fatalf("forecast calls = %d, want 1", got)
	}
}

func TestCacheLimit(t *testing.T) {
	now := time.Now()
	c := newFlightCache[int](context.Background(), time.Minute, maxCacheEntries, func() time.Time { return now })
	var calls atomic.Int64
	fetch := func(context.Context) (int, error) { calls.Add(1); return 1, nil }
	for i := range maxCacheEntries + 10 {
		_, _ = c.get(context.Background(), fmt.Sprint(i), fetch)
	}
	if s := c.stats(); s.size != maxCacheEntries {
		t.Fatalf("size = %d", s.size)
	}
	now = now.Add(2 * time.Minute)
	_, _ = c.get(context.Background(), "new", fetch)
	if s := c.stats(); s.size != 1 {
		t.Fatalf("after sweep size = %d, want 1", s.size)
	}
}

func TestCacheErrorsNotCached(t *testing.T) {
	c := newFlightCache[int](context.Background(), time.Minute, 4, time.Now)
	n := 0
	fetch := func(context.Context) (int, error) { n++; return 0, errors.New("boom") }
	_, _ = c.get(context.Background(), "k", fetch)
	_, _ = c.get(context.Background(), "k", fetch)
	if n != 2 || c.stats().errors != 2 {
		t.Fatalf("n=%d stats=%+v", n, c.stats())
	}
}

func TestCommands(t *testing.T) {
	cmds := New().Commands()
	if len(cmds) != 1 || cmds[0].Name != "weather" {
		t.Fatalf("commands = %+v", cmds)
	}
	opts := cmds[0].Options
	if len(opts) != 3 || opts[0].Name != "location" || !opts[0].Required || opts[0].MaxLength != maxLocationLen {
		t.Fatalf("location option = %+v", opts[0])
	}
	if opts[1].Name != "forecast" || opts[1].Type != discordgo.ApplicationCommandOptionBoolean || opts[1].Required {
		t.Errorf("forecast option = %+v", opts[1])
	}
	if opts[2].Name != "days" || *opts[2].MinValue != 1 || opts[2].MaxValue != 16 || opts[2].Required {
		t.Errorf("days option = %+v", opts[2])
	}
}

func TestComposeDiscord(t *testing.T) {
	m, f := newTestModule(t)
	ctx := context.Background()
	out := m.composeDiscord(ctx, "Winterberg", true, 10)
	if !strings.Contains(out, "Nordrhein-Westfalen") || strings.Count(out, "🌡️") != 10 || !strings.HasSuffix(out, attribution) {
		t.Errorf("forecast:\n%s", out)
	}
	f.geocodeBody = `{}`
	if out := m.composeDiscord(ctx, "No`where", false, 7); out != "Ort nicht gefunden: `No'where`" {
		t.Errorf("not found = %q", out)
	}
	f.forecastStatus = 500
	if out := m.composeDiscord(ctx, "Winterberg", false, 7); !strings.Contains(out, "konnte nicht abgerufen") {
		t.Errorf("error = %q", out)
	}
}

func TestInvalidLocationResponseEphemeral(t *testing.T) {
	r := invalidLocationResponse()
	if r.Data.Flags&discordgo.MessageFlagsEphemeral == 0 || r.Type != discordgo.InteractionResponseChannelMessageWithSource {
		t.Fatalf("response = %+v", r)
	}
}

func TestFetchSurvivesFirstCallerCancel(t *testing.T) {
	c := newFlightCache[int](context.Background(), time.Minute, 4, time.Now)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	v, err := c.get(ctx, "k", func(fctx context.Context) (int, error) { return 7, fctx.Err() })
	if err != nil || v != 7 {
		t.Fatalf("v=%d err=%v", v, err)
	}
}

func TestShutdownCancelsLookups(t *testing.T) {
	m, f := newTestModule(t)
	f.delay = 200 * time.Millisecond
	errc := make(chan error, 1)
	go func() {
		_, _, err := m.lookup(context.Background(), "x", true, 1, 1, 7)
		errc <- err
	}()
	time.Sleep(20 * time.Millisecond)
	if err := m.Shutdown(); err != nil {
		t.Fatal(err)
	}
	if err := <-errc; !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want canceled", err)
	}
}
