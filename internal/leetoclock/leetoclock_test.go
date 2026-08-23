package leetoclock

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/bot"
	"github.com/toksikk/gidbig/internal/cfg"
	"github.com/toksikk/gidbig/internal/leetoclock/util/datastore"
)

func TestModuleInterface(t *testing.T) {
	var _ bot.Module = New()
}

func TestModuleShape(t *testing.T) {
	m := New()
	if m.Name() != "leetoclock" {
		t.Fatalf("Name() = %q, want leetoclock", m.Name())
	}
	if len(m.Commands()) != 0 || len(m.Components()) != 0 {
		t.Fatal("leetoclock should not expose commands or components")
	}
	if len(m.Listeners()) != 1 {
		t.Fatalf("Listeners() len = %d, want 1", len(m.Listeners()))
	}
	if len(m.Background()) != 2 {
		t.Fatalf("Background() len = %d, want 2", len(m.Background()))
	}
}

func TestBackgroundTasksCancel(t *testing.T) {
	m := New()
	m.tickInterval = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, task := range m.Background() {
		done := make(chan struct{})
		go func(run func(context.Context)) {
			run(ctx)
			close(done)
		}(task.Run)
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatalf("background task %q did not stop", task.Name)
		}
	}
}

func TestShutdownWaitsForMessageWork(t *testing.T) {
	m, session := newTestModule(t)
	target := time.Date(2026, time.August, 13, 13, 37, 0, 0, time.Local)
	m.now = func() time.Time { return target }
	m.messageTimestamp = func(string) time.Time { return target }
	m.updateTarget()

	workStarted := make(chan struct{})
	releaseWork := make(chan struct{})
	m.renewGame = func(datastore.Game) {
		close(workStarted)
		<-releaseWork
	}
	m.onMessageCreate(session, &discordgo.MessageCreate{Message: &discordgo.Message{
		ID: "message", ChannelID: "channel", GuildID: "guild", Author: &discordgo.User{ID: "player"},
	}})
	<-workStarted

	done := make(chan error, 1)
	go func() { done <- m.Shutdown() }()
	select {
	case err := <-done:
		t.Fatalf("Shutdown returned before message work finished: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(releaseWork)
	if err := <-done; err != nil {
		t.Fatalf("Shutdown() = %v", err)
	}
}

func TestMessageDispatchPersistsGameState(t *testing.T) {
	m, session := newTestModule(t)
	target := time.Date(2026, time.August, 13, 13, 37, 0, 0, time.Local)
	m.now = func() time.Time { return target }
	m.messageTimestamp = func(string) time.Time { return target.Add(337 * time.Millisecond) }
	m.renewGame = func(datastore.Game) {}
	m.updateTarget()

	m.onMessageCreate(session, &discordgo.MessageCreate{Message: &discordgo.Message{
		ID: "message", ChannelID: "channel", GuildID: "guild", Author: &discordgo.User{ID: "player"},
	}})

	players, err := m.store.GetPlayers()
	if err != nil || len(players) != 1 || players[0].UserID != "player" {
		t.Fatalf("players = %#v, err = %v", players, err)
	}
	games, err := m.store.GetGames()
	if err != nil || len(games) != 1 || games[0].ChannelID != "channel" {
		t.Fatalf("games = %#v, err = %v", games, err)
	}
	scores, err := m.store.GetScores()
	if err != nil || len(scores) != 1 || scores[0].Score != 337 {
		t.Fatalf("scores = %#v, err = %v", scores, err)
	}
	seasons, err := m.store.GetSeasons()
	if err != nil || len(seasons) != 1 {
		t.Fatalf("seasons = %#v, err = %v", seasons, err)
	}
}

func TestMessageDispatchIgnoresInvalidMessages(t *testing.T) {
	tests := []struct {
		name      string
		author    *discordgo.User
		timestamp time.Time
	}{
		{name: "bot", author: &discordgo.User{ID: "other", Bot: true}},
		{name: "self", author: &discordgo.User{ID: "bot"}},
		{name: "outside window", author: &discordgo.User{ID: "other"}, timestamp: time.Date(2026, time.August, 13, 12, 0, 0, 0, time.Local)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, session := newTestModule(t)
			target := time.Date(2026, time.August, 13, 13, 37, 0, 0, time.Local)
			m.now = func() time.Time { return target }
			timestamp := tc.timestamp
			if timestamp.IsZero() {
				timestamp = target
			}
			m.messageTimestamp = func(string) time.Time { return timestamp }
			m.renewGame = func(datastore.Game) {}
			m.updateTarget()
			m.onMessageCreate(session, &discordgo.MessageCreate{Message: &discordgo.Message{
				ID: "message", ChannelID: "channel", GuildID: "guild", Author: tc.author,
			}})
			scores, err := m.store.GetScores()
			if err != nil || len(scores) != 0 {
				t.Fatalf("scores = %#v, err = %v", scores, err)
			}
		})
	}
}

func TestSortScoreArrayByScore(t *testing.T) {
	scores := []datastore.Score{{Score: 300, MessageID: "c"}, {Score: 100, MessageID: "a"}, {Score: 200, MessageID: "b"}}
	got := sortScoreArrayByScore(scores)
	for i, want := range []string{"a", "b", "c"} {
		if got[i].MessageID != want {
			t.Errorf("index %d: MessageID = %q, want %q", i, got[i].MessageID, want)
		}
	}
}

func TestInitAppliesLeetoclockConfig(t *testing.T) {
	newInitModule := func(t *testing.T, lec cfg.LeetoclockConfig) *Module {
		t.Helper()
		conf := &cfg.Config{}
		conf.Database.Path = filepath.Join(t.TempDir(), "gidbig.db")
		conf.Leetoclock = lec
		session, err := discordgo.New("Bot test")
		if err != nil {
			t.Fatal(err)
		}
		m := New()
		if err := m.Init(bot.Deps{Session: session, Config: conf}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := m.Shutdown(); err != nil {
				t.Errorf("Shutdown() = %v", err)
			}
		})
		return m
	}

	t.Run("announcement channels", func(t *testing.T) {
		m := newInitModule(t, cfg.LeetoclockConfig{AnnouncementChannels: []string{"chan1", "chan2"}})
		got := m.announcementChannels
		want := []string{"chan1", "chan2"}
		if len(got) != len(want) {
			t.Fatalf("announcementChannels = %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("announcementChannels[%d] = %q, want %q", i, got[i], want[i])
			}
		}
	})

	t.Run("debug mode sets fast tick interval and debug channel", func(t *testing.T) {
		m := newInitModule(t, cfg.LeetoclockConfig{Debug: true, DebugChannel: "debugchan"})
		if m.tickInterval != time.Second {
			t.Errorf("tickInterval = %v, want %v", m.tickInterval, time.Second)
		}
		if len(m.announcementChannels) != 1 || m.announcementChannels[0] != "debugchan" {
			t.Errorf("announcementChannels = %v, want [debugchan]", m.announcementChannels)
		}
	})

	t.Run("debug channel ignored when debug disabled", func(t *testing.T) {
		m := newInitModule(t, cfg.LeetoclockConfig{DebugChannel: "debugchan"})
		if len(m.announcementChannels) != 0 {
			t.Errorf("announcementChannels = %v, want empty", m.announcementChannels)
		}
	})

	t.Run("debug mode skips announcement channels", func(t *testing.T) {
		m := newInitModule(t, cfg.LeetoclockConfig{Debug: true, DebugChannel: "debugchan", AnnouncementChannels: []string{"prodchan"}})
		if len(m.announcementChannels) != 1 || m.announcementChannels[0] != "debugchan" {
			t.Errorf("announcementChannels = %v, want [debugchan]", m.announcementChannels)
		}
	})

	t.Run("omitted config leaves no channels", func(t *testing.T) {
		m := newInitModule(t, cfg.LeetoclockConfig{})
		if len(m.announcementChannels) != 0 {
			t.Errorf("announcementChannels = %v, want empty", m.announcementChannels)
		}
	})
}

func newTestModule(t *testing.T) (*Module, *discordgo.Session) {
	t.Helper()
	conf := &cfg.Config{}
	conf.Database.Path = filepath.Join(t.TempDir(), "gidbig.db")
	session, err := discordgo.New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	session.State.User = &discordgo.User{ID: "bot"}
	m := New()
	if err := m.Init(bot.Deps{Session: session, Config: conf}); err != nil {
		t.Fatal(err)
	}
	m.reactOnMessage = func(*discordgo.Session, string, string, string, string) {}
	t.Cleanup(func() {
		if err := m.Shutdown(); err != nil {
			t.Errorf("Shutdown() = %v", err)
		}
	})
	return m, session
}
