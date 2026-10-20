package logwindow

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

func newLogger(w *Window, lv slog.Leveler) (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	inner := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: lv})
	return slog.New(w.Handler(inner)), &buf
}

func TestWindowWrapsAround(t *testing.T) {
	w := New(3)
	log, _ := newLogger(w, slog.LevelDebug)
	for i := range 5 {
		log.Info(fmt.Sprintf("m%d", i))
	}
	lines := w.Lines(Query{})
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3", len(lines))
	}
	for i, want := range []string{"m2", "m3", "m4"} {
		if !strings.HasSuffix(lines[i], " INFO "+want) {
			t.Errorf("line %d = %q, want suffix %q", i, lines[i], want)
		}
	}
}

func TestNewDefaultsCapacity(t *testing.T) {
	if got := New(0).Cap(); got != DefaultCapacity {
		t.Fatalf("Cap = %d, want %d", got, DefaultCapacity)
	}
}

func TestLinesLevelFloorFilterAndCount(t *testing.T) {
	w := New(10)
	log, _ := newLogger(w, slog.LevelDebug)
	log.Debug("dbg")
	log.Info("Hello one")
	log.Warn("warning")
	log.Info("HELLO two")
	log.Error("hello three")

	if got := w.Lines(Query{MinLevel: slog.LevelWarn}); len(got) != 2 {
		t.Errorf("warn floor got %v", got)
	}
	got := w.Lines(Query{Filter: "hello"})
	if len(got) != 3 {
		t.Fatalf("filter got %v", got)
	}
	got = w.Lines(Query{Filter: "hello", Count: 2})
	if len(got) != 2 || !strings.Contains(got[0], "HELLO two") || !strings.Contains(got[1], "hello three") {
		t.Errorf("count got %v", got)
	}
}

func TestAttrsAndGroupsRender(t *testing.T) {
	w := New(4)
	log, _ := newLogger(w, slog.LevelInfo)
	log.With("mod", "x").WithGroup("g").Info("msg", "k", "two words", slog.Group("sub", "a", 1))
	lines := w.Lines(Query{})
	if len(lines) != 1 {
		t.Fatalf("lines = %v", lines)
	}
	want := `INFO msg mod=x g.k="two words" g.sub.a=1`
	if !strings.HasSuffix(lines[0], want) {
		t.Errorf("line = %q, want suffix %q", lines[0], want)
	}
}

func TestFormatUsesUTC(t *testing.T) {
	loc := time.FixedZone("x", 3600)
	e := Entry{Time: time.Date(2026, 1, 2, 3, 4, 5, 0, loc), Level: slog.LevelWarn, Message: "m"}
	if got, want := Format(e), "2026-01-02T02:04:05Z WARN m"; got != want {
		t.Errorf("Format = %q, want %q", got, want)
	}
}

func TestInnerStillReceivesAndEnabledFollowsLevelVar(t *testing.T) {
	var lv slog.LevelVar
	lv.Set(slog.LevelInfo)
	w := New(10)
	log, buf := newLogger(w, &lv)
	log.Debug("hidden")
	log.Info("shown")
	if !strings.Contains(buf.String(), "shown") {
		t.Error("inner handler did not receive the record")
	}
	if n := len(w.Lines(Query{MinLevel: slog.LevelDebug})); n != 1 {
		t.Errorf("window has %d lines, want 1", n)
	}
	lv.Set(slog.LevelDebug)
	log.Debug("now visible")
	if n := len(w.Lines(Query{MinLevel: slog.LevelDebug})); n != 2 {
		t.Errorf("window has %d lines after LevelVar change, want 2", n)
	}
}

func TestParseLevel(t *testing.T) {
	for in, want := range map[string]slog.Level{"debug": slog.LevelDebug, "INFO": slog.LevelInfo, "warn": slog.LevelWarn, "error": slog.LevelError} {
		if got, ok := ParseLevel(in); !ok || got != want {
			t.Errorf("ParseLevel(%q) = %v,%v", in, got, ok)
		}
	}
	if _, ok := ParseLevel("bogus"); ok {
		t.Error("bogus accepted")
	}
}

func TestConcurrentHandle(t *testing.T) {
	w := New(50)
	h := w.Handler(slog.NewTextHandler(&bytes.Buffer{}, nil))
	log := slog.New(h)
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 200 {
				log.Info("x", "g", g, "i", i)
				_ = w.Lines(Query{Count: 5})
			}
		}()
	}
	wg.Wait()
	if n := len(w.Lines(Query{})); n != 50 {
		t.Errorf("lines = %d, want 50", n)
	}
}
