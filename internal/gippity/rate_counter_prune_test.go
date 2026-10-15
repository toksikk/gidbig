package gippity

import (
	"context"
	"testing"
	"time"
)

func TestPruneCounters(t *testing.T) {
	m := New()
	m.userMessageCount = make(map[string]int)
	m.userMessageCountLastReset = make(map[string]time.Time)
	now := time.Date(2026, time.October, 15, 12, 0, 0, 0, time.UTC)
	m.userMessageCount["stale"] = 3
	m.userMessageCountLastReset["stale"] = now.Add(-time.Hour)
	m.userMessageCount["fresh"] = 2
	m.userMessageCountLastReset["fresh"] = now.Add(-59 * time.Minute)
	m.classifierCount["stale"] = 5
	m.classifierCountLastReset["stale"] = now.Add(-2 * time.Hour)
	m.classifierCount["fresh"] = 1
	m.classifierCountLastReset["fresh"] = now.Add(-time.Minute)

	m.pruneCounters(now)

	for name, mp := range map[string]map[string]int{"user": m.userMessageCount, "classifier": m.classifierCount} {
		if _, ok := mp["stale"]; ok {
			t.Errorf("%s count: stale entry kept", name)
		}
		if _, ok := mp["fresh"]; !ok {
			t.Errorf("%s count: fresh entry pruned", name)
		}
	}
	for name, mp := range map[string]map[string]time.Time{"user": m.userMessageCountLastReset, "classifier": m.classifierCountLastReset} {
		if _, ok := mp["stale"]; ok {
			t.Errorf("%s reset: stale entry kept", name)
		}
		if _, ok := mp["fresh"]; !ok {
			t.Errorf("%s reset: fresh entry pruned", name)
		}
	}
}

func TestRateCounterPruneLoopStopsOnCancel(t *testing.T) {
	m := New()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		m.rateCounterPruneLoop(ctx)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("prune loop did not stop on cancel")
	}
}
