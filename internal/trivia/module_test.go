package trivia

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/bot"
	"github.com/toksikk/gidbig/internal/cfg"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type sent struct {
	channelID string
	msg       *discordgo.MessageSend
}

type harness struct {
	m       *Module
	now     time.Time
	roll    float64
	mu      sync.Mutex
	sent    []sent
	genErr  error
	prompts []string
}

// newHarness builds an initialised module for guild "g" where users listed in
// optedIn have privacy off.
func newHarness(t *testing.T, optedIn ...string) *harness {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "gidbig.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	consent := map[string]bool{}
	for _, u := range optedIn {
		consent[u] = true
	}
	h := &harness{now: time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC), roll: 1}
	m := New(func(u string) bool { return !consent[u] })
	m.now = func() time.Time { return h.now }
	m.rand = func() float64 { return h.roll }
	m.shuffle = func(int, func(int, int)) {}
	m.name = func(_, userID string) string { return "name-" + userID }
	m.send = func(channelID string, msg *discordgo.MessageSend) error {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.sent = append(h.sent, sent{channelID, msg})
		return nil
	}
	m.generate = func(_ context.Context, system, user string) (string, error) {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.prompts = append(h.prompts, system+"|"+user)
		if h.genErr != nil {
			return "", h.genErr
		}
		return "  trivia: " + user + "  ", nil
	}
	conf := &cfg.Config{}
	conf.Gippity.AllowedGuilds = []string{"g"}
	conf.Trivia.DropinChance = 0.5
	conf.Trivia.DropinCooldown = "24h"
	if err := m.Init(bot.Deps{DB: db, Config: conf}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	h.m = m
	return h
}

func msg(id, guild, author, content string) *discordgo.MessageCreate {
	return &discordgo.MessageCreate{Message: &discordgo.Message{
		ID: id, GuildID: guild, ChannelID: "c", Content: content,
		Author: &discordgo.User{ID: author},
	}}
}

func (h *harness) total(t *testing.T, user, stat string) int64 {
	t.Helper()
	var sum int64
	h.m.store.db.Model(&DailyCount{}).Where("user_id = ? AND stat = ?", user, stat).
		Select("COALESCE(SUM(count),0)").Scan(&sum)
	return sum
}

func TestOnMessage_CountsOnlyConsentingUsersInAllowedGuilds(t *testing.T) {
	h := newHarness(t, "u1", "bot")
	h.m.onMessage(nil, msg("1", "g", "u1", "one two https://x.example"))
	h.m.onMessage(nil, msg("2", "other", "u1", "ignored guild"))
	h.m.onMessage(nil, msg("3", "g", "u2", "privacy on"))
	b := msg("4", "g", "bot", "bot")
	b.Author.Bot = true
	h.m.onMessage(nil, b)

	if got := h.total(t, "u1", statMessages); got != 1 {
		t.Errorf("u1 messages = %d, want 1", got)
	}
	if got := h.total(t, "u1", statWords); got != 2 {
		t.Errorf("u1 words = %d, want 2", got)
	}
	if got := h.total(t, "u1", statLinks); got != 1 {
		t.Errorf("u1 links = %d, want 1", got)
	}
	if got := h.total(t, "u2", statMessages); got != 0 {
		t.Errorf("privacy-on u2 counted: %d", got)
	}
	if got := h.total(t, "bot", statMessages); got != 0 {
		t.Errorf("bot counted: %d", got)
	}
	if a, _ := h.m.store.authorOf(context.Background(), "3"); a != "" {
		t.Errorf("privacy-on message indexed: %q", a)
	}
}

func reaction(guild, msgID, user string) *discordgo.MessageReaction {
	return &discordgo.MessageReaction{GuildID: guild, MessageID: msgID, UserID: user}
}

func TestReactions_GivenAndReceivedJudgedSeparately(t *testing.T) {
	h := newHarness(t, "author", "fan")
	h.m.onMessage(nil, msg("m1", "g", "author", "hi"))

	// Consenting reactor on consenting author: both sides count.
	h.m.onReactionAdd(nil, &discordgo.MessageReactionAdd{MessageReaction: reaction("g", "m1", "fan")})
	// Privacy-on reactor: no given, but the author still receives.
	h.m.onReactionAdd(nil, &discordgo.MessageReactionAdd{MessageReaction: reaction("g", "m1", "lurker")})
	// Self-reaction: given only.
	h.m.onReactionAdd(nil, &discordgo.MessageReactionAdd{MessageReaction: reaction("g", "m1", "author")})
	// Unindexed message: given only.
	h.m.onReactionAdd(nil, &discordgo.MessageReactionAdd{MessageReaction: reaction("g", "unknown", "fan")})
	// Bot reactor ignored.
	h.m.onReactionAdd(nil, &discordgo.MessageReactionAdd{
		MessageReaction: reaction("g", "m1", "fan"),
		Member:          &discordgo.Member{User: &discordgo.User{ID: "fan", Bot: true}},
	})
	// Other guild ignored.
	h.m.onReactionAdd(nil, &discordgo.MessageReactionAdd{MessageReaction: reaction("other", "m1", "fan")})

	if got := h.total(t, "fan", statReactionsGiven); got != 2 {
		t.Errorf("fan given = %d, want 2", got)
	}
	if got := h.total(t, "lurker", statReactionsGiven); got != 0 {
		t.Errorf("lurker given = %d, want 0", got)
	}
	if got := h.total(t, "author", statReactionsGiven); got != 1 {
		t.Errorf("author given = %d, want 1", got)
	}
	if got := h.total(t, "author", statReactionsReceived); got != 2 {
		t.Errorf("author received = %d, want 2", got)
	}

	h.m.onReactionRemove(nil, &discordgo.MessageReactionRemove{MessageReaction: reaction("g", "m1", "fan")})
	if got := h.total(t, "fan", statReactionsGiven); got != 1 {
		t.Errorf("fan given after remove = %d, want 1", got)
	}
	if got := h.total(t, "author", statReactionsReceived); got != 1 {
		t.Errorf("author received after remove = %d, want 1", got)
	}
}

func TestReactions_PrivacyOnAuthorReceivesNothing(t *testing.T) {
	h := newHarness(t, "fan")
	// Index the message directly, as if the author consented earlier.
	if err := h.m.store.indexAuthor(context.Background(), "m1", "g", "author", h.now); err != nil {
		t.Fatal(err)
	}
	h.m.onReactionAdd(nil, &discordgo.MessageReactionAdd{MessageReaction: reaction("g", "m1", "fan")})
	if got := h.total(t, "author", statReactionsReceived); got != 0 {
		t.Errorf("privacy-on author received = %d, want 0", got)
	}
}

func TestStats_ReportsCounters(t *testing.T) {
	h := newHarness(t, "u1")
	h.m.onMessage(nil, msg("1", "g", "u1", "hi"))
	st, err := h.m.Stats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Summary) == 0 || st.Summary[0].Value != "1" {
		t.Errorf("summary = %+v", st.Summary)
	}
}

func TestMemberDisplayName(t *testing.T) {
	cases := []struct {
		m    *discordgo.Member
		want string
	}{
		{nil, ""},
		{&discordgo.Member{Nick: "nick", User: &discordgo.User{GlobalName: "g", Username: "u"}}, "nick"},
		{&discordgo.Member{User: &discordgo.User{GlobalName: "g", Username: "u"}}, "g"},
		{&discordgo.Member{User: &discordgo.User{Username: "u"}}, "u"},
	}
	for _, c := range cases {
		if got := memberDisplayName(c.m); got != c.want {
			t.Errorf("memberDisplayName = %q, want %q", got, c.want)
		}
	}
}
