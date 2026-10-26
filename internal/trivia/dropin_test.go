package trivia

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// seedLeaders gives three consenting users messages so a drop-in has a leader.
func seedLeaders(t *testing.T, h *harness) {
	t.Helper()
	for i, u := range []string{"a", "b", "c"} {
		for j := 0; j <= i; j++ {
			h.m.onMessage(nil, msg(u+string(rune('0'+j)), "g", u, "x"))
		}
	}
}

// drain runs one queued job through the worker body, if any.
func drain(t *testing.T, h *harness) bool {
	t.Helper()
	select {
	case job := <-h.m.jobs:
		if err := h.m.postDropin(context.Background(), job); err != nil {
			h.m.release(context.Background(), job)
			return false
		}
		return true
	default:
		return false
	}
}

func TestDropin_TriggersAndPostsWithoutMentions(t *testing.T) {
	h := newHarness(t, "a", "b", "c")
	seedLeaders(t, h) // roll=1: no trigger while seeding
	if len(h.m.jobs) != 0 {
		t.Fatal("drop-in triggered above chance")
	}
	h.roll = 0.1
	h.m.onMessage(nil, msg("z", "g", "c", "x"))
	if !drain(t, h) {
		t.Fatal("expected a posted drop-in")
	}
	if len(h.sent) != 1 {
		t.Fatalf("sent = %d, want 1", len(h.sent))
	}
	s := h.sent[0]
	if s.channelID != "c" {
		t.Errorf("channel = %q", s.channelID)
	}
	if s.msg.AllowedMentions == nil || len(s.msg.AllowedMentions.Parse) != 0 {
		t.Errorf("allowed mentions not empty: %+v", s.msg.AllowedMentions)
	}
	// No shuffle: the first combo is messages/all-time; c leads with 4.
	if !strings.Contains(s.msg.Content, "name-c") || !strings.Contains(s.msg.Content, "with 4") {
		t.Errorf("content = %q", s.msg.Content)
	}
	if s.msg.Content != strings.TrimSpace(s.msg.Content) {
		t.Errorf("content not trimmed: %q", s.msg.Content)
	}

	// Within the cooldown nothing more triggers, even across a restart.
	h.now = h.now.Add(time.Hour)
	h.m.lastDropin = map[string]time.Time{}
	h.m.onMessage(nil, msg("z2", "g", "c", "x"))
	if len(h.m.jobs) != 0 {
		t.Fatal("drop-in triggered inside cooldown")
	}
	h.now = h.now.Add(24 * time.Hour)
	h.m.onMessage(nil, msg("z3", "g", "c", "x"))
	if len(h.m.jobs) != 1 {
		t.Fatal("drop-in not triggered after cooldown")
	}
}

func TestDropin_ZeroChanceNeverTriggers(t *testing.T) {
	h := newHarness(t, "a", "b", "c")
	h.m.chance = 0
	h.roll = 0
	seedLeaders(t, h)
	if len(h.m.jobs) != 0 {
		t.Fatal("drop-in triggered with chance 0")
	}
}

func TestDropin_FailuresReleaseCooldown(t *testing.T) {
	for _, tc := range []struct {
		name   string
		seed   bool
		genErr error
	}{
		{"too few participants", false, nil},
		{"llm error", true, errors.New("boom")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, "a", "b", "c")
			if tc.seed {
				seedLeaders(t, h)
			}
			h.genErr = tc.genErr
			h.roll = 0
			h.m.onMessage(nil, msg("z", "g", "a", "x"))
			if drain(t, h) {
				t.Fatal("expected the drop-in to fail")
			}
			if len(h.sent) != 0 {
				t.Fatal("message sent despite failure")
			}
			// Cooldown released: the next message may retry at once.
			h.m.onMessage(nil, msg("z2", "g", "a", "x"))
			if len(h.m.jobs) != 1 {
				t.Fatal("cooldown not released")
			}
			if last, _ := h.m.store.lastDropin(context.Background(), "g"); !last.IsZero() && !last.Equal(h.now) {
				t.Errorf("persisted cooldown = %v", last)
			}
		})
	}
}

func TestDropin_FullQueueReleasesClaim(t *testing.T) {
	h := newHarness(t, "a")
	h.m.jobs <- dropinJob{guildID: "busy"}
	h.roll = 0
	h.m.onMessage(nil, msg("z", "g", "a", "x"))
	h.m.mu.Lock()
	last := h.m.lastDropin["g"]
	h.m.mu.Unlock()
	if !last.IsZero() {
		t.Fatalf("claim kept with full queue: %v", last)
	}
}

func TestDropinWorker_StopsOnCancel(t *testing.T) {
	h := newHarness(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { h.m.runDropinWorker(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not stop")
	}
}
