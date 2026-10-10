package soundboard

import (
	"context"
	"testing"

	"github.com/bwmarrin/discordgo"
)

func TestStats(t *testing.T) {
	m := New()
	m.collections = []*soundCollection{{Prefix: "a", Sounds: []*soundClip{{Name: "x"}, {Name: "y"}}}}

	st, err := m.Stats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, s := range st.Summary {
		got[s.Name] = s.Value
	}
	if got["sounds"] != "2" || got["collections"] != "1" || got["queued"] != "0" {
		t.Errorf("summary = %#v", got)
	}
}

func TestCollections(t *testing.T) {
	m := New()
	m.collections = []*soundCollection{{Prefix: "memes", Sounds: []*soundClip{{Name: "wow"}, {Name: "nice"}}}}

	got := m.Collections()
	if len(got) != 1 || got[0].Prefix != "memes" {
		t.Fatalf("Collections() = %#v", got)
	}
	if len(got[0].Sounds) != 2 || got[0].Sounds[1].Name != "nice" {
		t.Errorf("sounds = %#v", got[0].Sounds)
	}
}

func TestQueueStatus(t *testing.T) {
	m := New()
	if got := m.QueueStatus(); len(got) != 0 {
		t.Fatalf("QueueStatus() = %#v, want empty", got)
	}

	m.queues["guild"] = make(chan *Play, 4)
	m.queues["guild"] <- &Play{Sound: &soundClip{Name: "beep"}}
	m.nowPlaying["guild"] = &Play{Sound: &soundClip{Name: "boom"}}

	got := m.QueueStatus()
	if len(got) != 1 {
		t.Fatalf("QueueStatus() len = %d, want 1", len(got))
	}
	if got[0].GuildID != "guild" || got[0].QueueLength != 1 || got[0].NowPlaying != "boom" {
		t.Errorf("QueueStatus()[0] = %#v", got[0])
	}
}

func TestEnqueue(t *testing.T) {
	m := New()
	m.collections = []*soundCollection{{Prefix: "memes", Commands: []string{"!memes"}, Sounds: []*soundClip{{Name: "wow"}}}}

	queued := make(chan struct{}, 1)
	m.enqueue = func(*discordgo.User, *discordgo.Guild, *soundCollection, *soundClip) { queued <- struct{}{} }

	user := &discordgo.User{ID: "u"}
	guild := &discordgo.Guild{ID: "g"}

	if m.Enqueue(nil, guild, "!memes", "wow") {
		t.Error("Enqueue with nil user returned true")
	}
	if m.Enqueue(user, nil, "!memes", "wow") {
		t.Error("Enqueue with nil guild returned true")
	}
	if m.Enqueue(user, guild, "!unknown", "wow") {
		t.Error("Enqueue with unknown collection returned true")
	}
	if !m.Enqueue(user, guild, "!memes", "wow") {
		t.Fatal("Enqueue with valid collection returned false")
	}
	select {
	case <-queued:
	default:
		t.Error("valid Enqueue did not queue a play")
	}
}
