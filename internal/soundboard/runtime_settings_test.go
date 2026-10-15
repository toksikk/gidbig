package soundboard

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/toksikk/gidbig/internal/cfg"
	"github.com/toksikk/gidbig/internal/settings"
	"github.com/toksikk/gidbig/internal/shareddb"
)

func newTestSettingsStore(t *testing.T) *settings.Store {
	t.Helper()
	db, err := shareddb.Open(filepath.Join(t.TempDir(), shareddb.Filename))
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	base, err := cfg.Decode(strings.NewReader("discord:\n  token: t\ngippity:\n  allowed_guilds: [guild]\n"))
	if err != nil {
		t.Fatalf("decode base config: %v", err)
	}
	store, err := settings.New(db, base)
	if err != nil {
		t.Fatalf("settings.New: %v", err)
	}
	return store
}

// TestQueueMaxDepth_ReflectsRuntimeSetting covers the single read point the
// enqueue path uses, so a runtime change applies without a restart.
func TestQueueMaxDepth_ReflectsRuntimeSetting(t *testing.T) {
	m := New()
	if got := m.queueMaxDepth(); got != defaultMaxQueueSize {
		t.Fatalf("queueMaxDepth() = %d, want the default %d", got, defaultMaxQueueSize)
	}

	store := newTestSettingsStore(t)
	m.settings = store

	if err := store.Set(settings.KeySoundboardQueueMaxDepth, "2", "owner"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if got := m.queueMaxDepth(); got != 2 {
		t.Errorf("queueMaxDepth() = %d, want 2", got)
	}

	// An Init-time value is overridden by the runtime setting.
	m.maxQueueSize = 6
	if got := m.queueMaxDepth(); got != 2 {
		t.Errorf("queueMaxDepth() with an Init-time value = %d, want the runtime 2", got)
	}

	if err := store.Unset(settings.KeySoundboardQueueMaxDepth, "owner"); err != nil {
		t.Fatalf("Unset: %v", err)
	}
	if got := m.queueMaxDepth(); got != 6 {
		t.Errorf("queueMaxDepth() after Unset = %d, want 6", got)
	}
}

func TestStats_MaxQueueUsesTheRuntimeSetting(t *testing.T) {
	m := New()
	store := newTestSettingsStore(t)
	m.settings = store
	if err := store.Set(settings.KeySoundboardQueueMaxDepth, "3", "owner"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	stats, err := m.Stats(context.Background())
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	for _, stat := range stats.Detail {
		if stat.Name == "max queue" {
			if stat.Value != "3" {
				t.Errorf("max queue stat = %q, want %q", stat.Value, "3")
			}
			return
		}
	}
	t.Fatal("no max queue stat reported")
}

// TestOffer_NeverBlocksOnAFullQueue is the guard against a deadlock the dynamic
// queue depth introduces: a channel is created once with the depth in force at
// the time, so raising soundboard.queue_max_depth at runtime leaves an existing
// channel full at its old capacity. "len < depth" is then true while there is no
// free slot, and a blocking send would hold the queue mutex that the player
// goroutine needs before it can drain — a permanent deadlock.
func TestOffer_NeverBlocksOnAFullQueue(t *testing.T) {
	m := New()

	full := &play{}
	m.queues["guild"] = make(chan *play, 1)
	m.queues["guild"] <- full
	// A raised depth must not turn this into a blocking send.
	if m.offer("guild", &play{}, 5) {
		t.Error("offer accepted a play into a channel that has no free slot")
	}
	if got := len(m.queues["guild"]); got != 1 {
		t.Errorf("queue length = %d, want the untouched 1", got)
	}
}

func TestOffer_AdmitsWhileThereIsRoom(t *testing.T) {
	m := New()
	m.queues["guild"] = make(chan *play, 3)
	for i := 0; i < 3; i++ {
		if !m.offer("guild", &play{}, 3) {
			t.Fatalf("offer refused entry %d below the depth", i)
		}
	}
	if m.offer("guild", &play{}, 3) {
		t.Error("offer accepted an entry past the depth")
	}
	// A missing guild must refuse rather than block on a nil channel.
	if m.offer("other-guild", &play{}, 3) {
		t.Error("offer accepted a play for a guild with no queue")
	}
}

// TestOffer_IgnoresDepthAboveTheChannelCapacity documents the supported
// behaviour after a runtime raise: the existing channel keeps its old capacity,
// so the effective limit is the smaller of the two until the queue drains and a
// new channel is created with the new depth.
func TestOffer_IgnoresDepthAboveTheChannelCapacity(t *testing.T) {
	m := New()
	m.queues["guild"] = make(chan *play, 2)
	if !m.offer("guild", &play{}, 4) {
		t.Fatal("offer refused the first entry within the channel capacity")
	}
	if !m.offer("guild", &play{}, 4) {
		t.Fatal("offer refused the second entry within the channel capacity")
	}
	if m.offer("guild", &play{}, 4) {
		t.Error("offer exceeded the channel capacity")
	}
}
