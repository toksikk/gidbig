// Package trivia counts per-user chat activity for consenting users and
// occasionally drops an LLM-phrased trivia line about one leader into chat.
package trivia

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/bot"
	"github.com/toksikk/gidbig/internal/settings"
)

// pruneInterval is how often the author index is pruned.
const pruneInterval = 24 * time.Hour

// handlerTimeout bounds the database work of one gateway event.
const handlerTimeout = 5 * time.Second

// Module implements bot.Module and bot.StatsProvider.
type Module struct {
	// privacy reports whether a user keeps /gippity privacy on. Only users
	// for whom it returns false are counted or named.
	privacy func(userID string) bool

	store    *store
	session  *discordgo.Session
	settings *settings.Store
	// allowedGuilds is the Init-time gippity.allowed_guilds snapshot, used
	// when no settings store is available.
	allowedGuilds map[string]bool

	chance   float64
	cooldown time.Duration

	// Injectable for tests.
	now      func() time.Time
	rand     func() float64
	shuffle  func(n int, swap func(i, j int))
	generate func(ctx context.Context, system, user string) (string, error)
	persona  func() string
	send     func(channelID string, msg *discordgo.MessageSend) error
	name     func(guildID, userID string) string

	mu         sync.Mutex
	lastDropin map[string]time.Time // guildID -> last claimed drop-in

	jobs chan dropinJob

	eventsCounted  atomic.Int64
	dropinsPosted  atomic.Int64
	dropinsSkipped atomic.Int64
}

// New creates the trivia module. privacy is the consent check; nil means
// nobody is counted.
func New(privacy func(userID string) bool) *Module {
	if privacy == nil {
		privacy = func(string) bool { return true }
	}
	return &Module{
		privacy:    privacy,
		now:        time.Now,
		rand:       rand.Float64,
		shuffle:    rand.Shuffle,
		lastDropin: make(map[string]time.Time),
		jobs:       make(chan dropinJob, 1),
	}
}

// Name returns the module name.
func (m *Module) Name() string { return "trivia" }

// Init migrates the trivia tables and registers the message and reaction
// listeners.
func (m *Module) Init(d bot.Deps) error {
	st, err := newStore(d.DB)
	if err != nil {
		return fmt.Errorf("trivia: open store: %w", err)
	}
	m.store = st
	m.session = d.Session
	m.settings = d.Settings
	m.allowedGuilds = make(map[string]bool)
	if d.Config != nil {
		for _, g := range d.Config.Gippity.AllowedGuilds {
			m.allowedGuilds[g] = true
		}
		m.chance = d.Config.Trivia.DropinChance
		m.cooldown = d.Config.TriviaDropinCooldown()
	}
	if d.LLM != nil {
		m.generate = d.LLM.GenerateMessage
		m.persona = d.LLM.Personality
	}
	if m.send == nil && d.Session != nil {
		m.send = func(channelID string, msg *discordgo.MessageSend) error {
			_, err := d.Session.ChannelMessageSendComplex(channelID, msg)
			return err
		}
	}
	if m.name == nil {
		m.name = m.displayName
	}
	d.Router.OnMessage(m.onMessage)
	d.Router.OnMessageReactionAdd(m.onReactionAdd)
	d.Router.OnMessageReactionRemove(m.onReactionRemove)
	slog.Info("trivia: initialized", "dropin_chance", m.chance, "dropin_cooldown", m.cooldown)
	return nil
}

// Background returns the drop-in worker and the author-index pruner.
func (m *Module) Background() []bot.BackgroundTask {
	return []bot.BackgroundTask{
		{Name: "trivia-dropin", Run: m.runDropinWorker},
		{Name: "trivia-prune", Run: m.runPruner},
	}
}

// Shutdown is a no-op; the shared database is owned by the composition root.
func (m *Module) Shutdown() error { return nil }

// Stats reports runtime counters for /status.
func (m *Module) Stats(_ context.Context) (bot.ModuleStats, error) {
	return bot.ModuleStats{
		Summary: []bot.Stat{
			{Name: "events counted", Value: strconv.FormatInt(m.eventsCounted.Load(), 10)},
			{Name: "drop-ins", Value: strconv.FormatInt(m.dropinsPosted.Load(), 10)},
		},
		Detail: []bot.Stat{
			{Name: "drop-ins skipped", Value: strconv.FormatInt(m.dropinsSkipped.Load(), 10)},
		},
	}, nil
}

// counted reports whether userID has consented (privacy off).
func (m *Module) counted(userID string) bool { return userID != "" && !m.privacy(userID) }

// guildAllowed reports whether guildID is in gippity.allowed_guilds, read
// from the settings store on every event so /admin config changes apply.
func (m *Module) guildAllowed(guildID string) bool {
	if guildID == "" {
		return false
	}
	if m.settings == nil {
		return m.allowedGuilds[guildID]
	}
	for _, id := range m.settings.StringList(settings.KeyGippityAllowedGuilds) {
		if id == guildID {
			return true
		}
	}
	return false
}

func (m *Module) botUserID() string {
	if m.session == nil || m.session.State == nil || m.session.State.User == nil {
		return ""
	}
	return m.session.State.User.ID
}

func (m *Module) onMessage(_ *discordgo.Session, mc *discordgo.MessageCreate) {
	if mc == nil || mc.Message == nil || mc.Author == nil || mc.Author.Bot || m.store == nil {
		return
	}
	if !m.guildAllowed(mc.GuildID) || !m.counted(mc.Author.ID) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), handlerTimeout)
	defer cancel()

	now := m.now()
	c := classifyMessage(mc.Message)
	deltas := []struct {
		stat string
		n    int
	}{
		{statMessages, 1},
		{statWords, c.Words},
		{statGIFs, c.GIFs},
		{statPictures, c.Pictures},
		{statLinks, c.Links},
	}
	for _, d := range deltas {
		if err := m.store.add(ctx, mc.GuildID, mc.Author.ID, now, d.stat, int64(d.n)); err != nil {
			slog.Warn("trivia: count message", "guild", mc.GuildID, "stat", d.stat, "error", err)
			return
		}
	}
	if err := m.store.indexAuthor(ctx, mc.ID, mc.GuildID, mc.Author.ID, now); err != nil {
		slog.Warn("trivia: index message author", "guild", mc.GuildID, "error", err)
	}
	m.eventsCounted.Add(1)
	m.maybeTrigger(ctx, mc.GuildID, mc.ChannelID)
}

func (m *Module) onReactionAdd(_ *discordgo.Session, r *discordgo.MessageReactionAdd) {
	if r == nil || r.MessageReaction == nil {
		return
	}
	if r.Member != nil && r.Member.User != nil && r.Member.User.Bot {
		return
	}
	m.countReaction(r.MessageReaction, 1)
}

func (m *Module) onReactionRemove(_ *discordgo.Session, r *discordgo.MessageReactionRemove) {
	if r == nil || r.MessageReaction == nil {
		return
	}
	// Removals carry no member, so a bot reactor is not detectable here; the
	// consent check still drops it, since bots cannot turn privacy off.
	m.countReaction(r.MessageReaction, -1)
}

// countReaction credits a reaction as given to the reactor and as received
// to the indexed message author. Each side is judged by its own consent.
func (m *Module) countReaction(r *discordgo.MessageReaction, delta int64) {
	if m.store == nil || r.UserID == "" || r.UserID == m.botUserID() || !m.guildAllowed(r.GuildID) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), handlerTimeout)
	defer cancel()
	now := m.now()

	if m.counted(r.UserID) {
		if err := m.store.add(ctx, r.GuildID, r.UserID, now, statReactionsGiven, delta); err != nil {
			slog.Warn("trivia: count reaction given", "guild", r.GuildID, "error", err)
		} else {
			m.eventsCounted.Add(1)
		}
	}

	author, err := m.store.authorOf(ctx, r.MessageID)
	if err != nil {
		slog.Warn("trivia: look up reacted message", "guild", r.GuildID, "error", err)
		return
	}
	if author == "" || author == r.UserID || !m.counted(author) {
		return
	}
	if err := m.store.add(ctx, r.GuildID, author, now, statReactionsReceived, delta); err != nil {
		slog.Warn("trivia: count reaction received", "guild", r.GuildID, "error", err)
		return
	}
	m.eventsCounted.Add(1)
}

func (m *Module) runPruner(ctx context.Context) {
	prune := func() {
		if m.store == nil {
			return
		}
		cutoff := m.now().Add(-windowDays * 24 * time.Hour)
		n, err := m.store.pruneAuthors(ctx, cutoff)
		if err != nil {
			slog.Warn("trivia: prune author index", "error", err)
			return
		}
		if n > 0 {
			slog.Info("trivia: pruned author index", "rows", n)
		}
	}
	prune()
	t := time.NewTicker(pruneInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			prune()
		}
	}
}

// displayName resolves a member's guild nickname, then global name, then
// username, without mentioning them.
func (m *Module) displayName(guildID, userID string) string {
	if m.session == nil {
		return ""
	}
	var member *discordgo.Member
	if m.session.State != nil {
		member, _ = m.session.State.Member(guildID, userID)
	}
	if member == nil {
		var err error
		member, err = m.session.GuildMember(guildID, userID)
		if err != nil {
			slog.Warn("trivia: resolve member", "guild", guildID, "error", err)
			return ""
		}
	}
	return memberDisplayName(member)
}

func memberDisplayName(member *discordgo.Member) string {
	if member == nil {
		return ""
	}
	if member.Nick != "" {
		return member.Nick
	}
	if member.User == nil {
		return ""
	}
	if member.User.GlobalName != "" {
		return member.User.GlobalName
	}
	return member.User.Username
}
