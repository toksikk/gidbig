package leetoclock

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/bot"
	"github.com/toksikk/gidbig/internal/cfg"
	"github.com/toksikk/gidbig/internal/leetoclock/util/datastore"
	"github.com/toksikk/gidbig/internal/settings"
	"github.com/toksikk/gidbig/internal/util"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// openTestDB returns a migrated shared database for tests that inject Deps.DB.
func openTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "gidbig.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, dbErr := db.DB(); dbErr == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func TestModuleInterface(t *testing.T) {
	var _ bot.Module = New()
}

func TestModuleShape(t *testing.T) {
	m := New()
	if m.Name() != "leetoclock" {
		t.Fatalf("Name() = %q, want leetoclock", m.Name())
	}
	if len(m.Commands()) != 1 {
		t.Fatal("leetoclock should expose one command")
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
		conf.Leetoclock = lec
		session, err := discordgo.New("Bot test")
		if err != nil {
			t.Fatal(err)
		}
		m := New()
		if err := m.Init(bot.Deps{Session: session, Config: conf, DB: openTestDB(t)}); err != nil {
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

func TestResolveEmoji(t *testing.T) {
	tests := []struct {
		name       string
		configured string
		fallback   string
		want       string
	}{
		{name: "empty uses fallback", configured: "", fallback: "😴", want: "😴"},
		{name: "numeric id formatted", configured: "750630908372975636", fallback: "😴", want: "zonk:750630908372975636"},
		{name: "unicode used verbatim", configured: "🧐", fallback: "😴", want: "🧐"},
		{name: "name id used verbatim", configured: "custom:123", fallback: "😴", want: "custom:123"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveEmoji("zonk", tc.configured, tc.fallback); got != tc.want {
				t.Errorf("resolveEmoji() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestInitAppliesEmojiConfig(t *testing.T) {
	newInitModule := func(t *testing.T, lec cfg.LeetoclockConfig) *Module {
		t.Helper()
		conf := &cfg.Config{}
		conf.Leetoclock = lec
		session, err := discordgo.New("Bot test")
		if err != nil {
			t.Fatal(err)
		}
		m := New()
		if err := m.Init(bot.Deps{Session: session, Config: conf, DB: openTestDB(t)}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := m.Shutdown(); err != nil {
				t.Errorf("Shutdown() = %v", err)
			}
		})
		return m
	}

	t.Run("defaults when omitted", func(t *testing.T) {
		m := newInitModule(t, cfg.LeetoclockConfig{})
		if m.emojiZonk != fallbackZonk || m.emojiLol != fallbackLol ||
			m.emojiNotAmused != fallbackNotAmused || m.emojiWat != fallbackWat {
			t.Errorf("emojis = %q/%q/%q/%q, want fallbacks", m.emojiZonk, m.emojiLol, m.emojiNotAmused, m.emojiWat)
		}
	})

	t.Run("custom emoji ids", func(t *testing.T) {
		m := newInitModule(t, cfg.LeetoclockConfig{Emojis: cfg.LeetoclockEmojisConfig{
			Zonk: "111", Lol: "222", NotAmused: "333", Wat: "444",
		}})
		want := []string{"zonk:111", "lol:222", "notamused:333", "wat:444"}
		got := []string{m.emojiZonk, m.emojiLol, m.emojiNotAmused, m.emojiWat}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("emoji[%d] = %q, want %q", i, got[i], want[i])
			}
		}
	})
}

func TestAnnouncementTitle(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		m := New()
		m.season = func() util.Season { return util.SeasonNone }
		if got := m.announcementTitle(); got != "## Leet o'Clock scheduled:" {
			t.Fatalf("announcementTitle() = %q, want default title", got)
		}
	})

	t.Run("easter", func(t *testing.T) {
		m := New()
		m.season = func() util.Season { return util.SeasonEaster }
		if got := m.announcementTitle(); got != "## 🐣 Leet o'Clock Osterei-Jagd scheduled:" {
			t.Fatalf("announcementTitle() = %q, want Easter title", got)
		}
	})
}

func newTestModule(t *testing.T) (*Module, *discordgo.Session) {
	t.Helper()
	conf := &cfg.Config{}
	session, err := discordgo.New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	session.State.User = &discordgo.User{ID: "bot"}
	m := New()
	if err := m.Init(bot.Deps{Session: session, Config: conf, DB: openTestDB(t)}); err != nil {
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

func TestApplyDebug(t *testing.T) {
	m := New()
	fixed := time.Date(2026, 1, 2, 9, 15, 30, 0, time.UTC)
	m.now = func() time.Time { return fixed }
	m.debugChannel = "debugchan"
	m.baseChannels = []string{"prod1", "prod2"}

	m.applyDebug(true)
	if m.targetHour != 9 || m.targetMinute != 16 {
		t.Errorf("debug target = %02d:%02d, want 09:16", m.targetHour, m.targetMinute)
	}
	if m.tickInterval != time.Second {
		t.Errorf("debug tick = %v, want 1s", m.tickInterval)
	}
	if got := m.channels(); len(got) != 1 || got[0] != "debugchan" {
		t.Errorf("debug channels = %v, want [debugchan]", got)
	}

	m.applyDebug(false)
	if m.targetHour != defaultHour || m.targetMinute != defaultMinute {
		t.Errorf("normal target = %02d:%02d, want 13:37", m.targetHour, m.targetMinute)
	}
	if m.tickInterval != time.Minute {
		t.Errorf("normal tick = %v, want 1m", m.tickInterval)
	}
	if got := m.channels(); len(got) != 2 || got[0] != "prod1" || got[1] != "prod2" {
		t.Errorf("normal channels = %v, want [prod1 prod2]", got)
	}
}

func TestDebugFlipTakesEffectBetweenIterations(t *testing.T) {
	db := openTestDB(t)
	conf, err := cfg.Decode(strings.NewReader("discord:\n  token: t\ngippity:\n  allowed_guilds: [g]\nleetoclock:\n  debug_channel: debugchan\n  announcement_channels: [prod]\n"))
	if err != nil {
		t.Fatalf("decode config: %v", err)
	}
	store, err := settings.New(db, conf)
	if err != nil {
		t.Fatalf("settings.New: %v", err)
	}
	session, err := discordgo.New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	m := New()
	fixed := time.Date(2026, 1, 2, 9, 15, 30, 0, time.UTC)
	m.now = func() time.Time { return fixed }
	if err := m.Init(bot.Deps{Session: session, Config: conf, DB: db, Settings: store}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Shutdown() })

	if m.currentTick() != time.Minute || m.currentTarget().Hour() != defaultHour {
		t.Fatalf("initial state not normal: tick=%v target=%v", m.currentTick(), m.currentTarget())
	}

	if err := store.Set(settings.KeyLeetoclockDebug, "true", "owner"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	m.syncDebug()
	m.updateTarget()
	if m.currentTick() != time.Second {
		t.Errorf("tick after enable = %v, want 1s", m.currentTick())
	}
	if tg := m.currentTarget(); tg.Hour() != 9 || tg.Minute() != 16 {
		t.Errorf("target after enable = %v, want 09:16", tg)
	}
	if got := m.channels(); len(got) != 1 || got[0] != "debugchan" {
		t.Errorf("channels after enable = %v", got)
	}

	// A further iteration without a change must not push the target forward.
	fixed = fixed.Add(30 * time.Second)
	m.syncDebug()
	m.updateTarget()
	if tg := m.currentTarget(); tg.Minute() != 16 {
		t.Errorf("target moved without a flag change: %v", tg)
	}

	if err := store.Set(settings.KeyLeetoclockDebug, "false", "owner"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	m.syncDebug()
	m.updateTarget()
	if m.currentTick() != time.Minute || m.currentTarget().Hour() != defaultHour || m.currentTarget().Minute() != defaultMinute {
		t.Errorf("state after disable: tick=%v target=%v", m.currentTick(), m.currentTarget())
	}
	if got := m.channels(); len(got) != 1 || got[0] != "prod" {
		t.Errorf("channels after disable = %v", got)
	}
}

func TestDebugConcurrentReadsRace(t *testing.T) {
	m := New()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			m.applyDebug(i%2 == 0)
		}
	}()
	syncDone := make(chan struct{})
	go func() {
		defer close(syncDone)
		for i := 0; i < 200; i++ {
			m.syncDebug()
		}
	}()
	for i := 0; i < 200; i++ {
		m.updateTarget()
		_ = m.channels()
		_ = m.currentTick()
		_ = m.isOnTargetTimeRange(time.Now(), false)
	}
	<-done
	<-syncDone
}

func TestSyncDebugAppliesOnceAcrossMinuteBoundary(t *testing.T) {
	m := New()
	var clockMu sync.Mutex
	clock := time.Date(2026, 1, 2, 9, 15, 59, 0, time.UTC)
	m.now = func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		t := clock
		clock = clock.Add(time.Second) // every read crosses into the next minute
		return t
	}
	m.cfgDebug = true

	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.syncDebug()
		}()
	}
	wg.Wait()

	// Exactly one apply reads the clock (09:15:59 -> 09:16:59 -> 09:16).
	if tg := m.currentTarget(); tg.Hour() != 9 || tg.Minute() != 16 {
		t.Errorf("target = %v, want 09:16 (applied once)", tg)
	}
	clockMu.Lock()
	reads := clock.Sub(time.Date(2026, 1, 2, 9, 15, 59, 0, time.UTC))
	clockMu.Unlock()
	if reads != time.Second {
		t.Errorf("debug applied %d times, want once", int(reads/time.Second))
	}
}

func TestDebugTargetCrossesMidnight(t *testing.T) {
	m := New()
	fixed := time.Date(2026, 1, 2, 23, 59, 30, 0, time.UTC)
	m.now = func() time.Time { return fixed }
	m.applyDebug(true)
	m.updateTarget()
	want := time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)
	if tg := m.currentTarget(); !tg.Equal(want) {
		t.Fatalf("target = %v, want %v", tg, want)
	}
	if !m.isOnTargetTimeRange(fixed, false) {
		t.Error("23:59:30 should be in the preparation window")
	}
	fixed = time.Date(2026, 1, 3, 0, 0, 10, 0, time.UTC)
	m.updateTarget()
	if tg := m.currentTarget(); !tg.Equal(want) {
		t.Fatalf("target after midnight = %v, want %v", tg, want)
	}
	if !m.isOnTargetTimeRange(fixed, true) {
		t.Error("00:00:10 next day should fire the debug round")
	}
	if got := m.calculateScore(fixed); got != 10000 {
		t.Errorf("score = %d, want 10000", got)
	}
}
