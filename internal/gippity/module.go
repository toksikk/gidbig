package gippity

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/bot"
	"github.com/toksikk/gidbig/internal/llm"
	"github.com/toksikk/gidbig/internal/util"

	openai "github.com/openai/openai-go/v3"
)

// Module implements bot.Module, bot.AdminProvider and bot.StatsProvider for the
// gippity plugin. All former package-level state now lives on this struct.
type Module struct {
	session *discordgo.Session

	db     *sql.DB
	dbMu   sync.Mutex
	dbPath string

	// stateMu guards idToNameCache and the per-user mention counters, which are
	// touched by concurrent Discord handlers and the cache-reset background task.
	stateMu sync.Mutex

	idToNameCache map[string]string

	allowedGuildIDs map[string]bool
	ignoredUserIDs  map[string]bool

	userMessageCount          map[string]int
	userMessageLimit          int
	userMessageCountLastReset map[string]time.Time

	// Test hooks. Production defaults are wired in New.
	generateAnswerFunc         func(*discordgo.MessageCreate, []string) (string, error)
	chatCompletionFunc         func(context.Context, openai.ChatCompletionNewParams) (*openai.ChatCompletion, error)
	channelTypingFunc          func(*discordgo.Session, string)
	describeImagesFunc         func([]string) (string, error)
	visionCompletionFunc       func(context.Context, openai.ChatCompletionNewParams) (*openai.ChatCompletion, error)
	fetchReferencedMessageFunc func(*discordgo.Session, *discordgo.MessageReference) (*discordgo.Message, error)
	channelMessageFunc         func(*discordgo.Session, string, string) (*discordgo.Message, error)
	fetchMessageReactionsFunc  func(*discordgo.Session, string, string) (*discordgo.Message, error)
	seasonFunc                 func() util.Season
}

// New returns a Module with production-default hook implementations.
func New() *Module {
	m := &Module{
		dbPath:           chatHistoryDBFilename,
		idToNameCache:    make(map[string]string),
		userMessageLimit: 30,
	}
	m.generateAnswerFunc = m.generateAnswer
	m.chatCompletionFunc = func(ctx context.Context, params openai.ChatCompletionNewParams) (*openai.ChatCompletion, error) {
		return llm.GetClient().Chat.Completions.New(ctx, params)
	}
	m.channelTypingFunc = func(s *discordgo.Session, channelID string) {
		s.ChannelTyping(channelID) //nolint:errcheck
	}
	m.describeImagesFunc = m.describeImages
	m.visionCompletionFunc = func(ctx context.Context, params openai.ChatCompletionNewParams) (*openai.ChatCompletion, error) {
		return llm.GetClient().Chat.Completions.New(ctx, params)
	}
	m.channelMessageFunc = func(s *discordgo.Session, channelID, messageID string) (*discordgo.Message, error) {
		return s.ChannelMessage(channelID, messageID)
	}
	m.fetchMessageReactionsFunc = func(s *discordgo.Session, channelID, messageID string) (*discordgo.Message, error) {
		return m.channelMessageFunc(s, channelID, messageID)
	}
	m.fetchReferencedMessageFunc = m.fetchReferencedMessage
	m.seasonFunc = util.CurrentSeason
	return m
}

// Name returns the module's identifier.
func (m *Module) Name() string { return "gippity" }

// Init wires dependencies and opens the chat history database.
func (m *Module) Init(d bot.Deps) error {
	m.session = d.Session

	if d.Config != nil && d.Config.Gippity.RateLimitMessagesPerHour > 0 {
		m.userMessageLimit = d.Config.Gippity.RateLimitMessagesPerHour
	}

	m.userMessageCount = make(map[string]int)
	m.userMessageCountLastReset = make(map[string]time.Time)

	m.allowedGuildIDs = make(map[string]bool)
	m.ignoredUserIDs = make(map[string]bool)
	if d.Config != nil {
		for _, id := range d.Config.Gippity.AllowedGuilds {
			m.allowedGuildIDs[id] = true
		}
		for _, id := range d.Config.Gippity.IgnoredUsers {
			m.ignoredUserIDs[id] = true
		}
	}

	if err := m.initDB(); err != nil {
		return fmt.Errorf("gippity: init db: %w", err)
	}

	slog.Info("gippity: initialized")
	return nil
}

// Commands returns the slash command definitions owned by this module.
func (m *Module) Commands() []*discordgo.ApplicationCommand {
	return []*discordgo.ApplicationCommand{
		{
			Name:        "gippity",
			Description: "Gippity settings",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "privacy",
					Description: "Control whether your past messages are anonymized in AI context",
					Options: []*discordgo.ApplicationCommandOption{
						{
							Type:        discordgo.ApplicationCommandOptionString,
							Name:        "set",
							Description: "on = anonymize (default), off = include as-is",
							Required:    true,
							Choices: []*discordgo.ApplicationCommandOptionChoice{
								{Name: "on", Value: "on"},
								{Name: "off", Value: "off"},
							},
						},
					},
				},
			},
		},
	}
}

// Listeners returns the Discord event listeners for this module.
func (m *Module) Listeners() []bot.EventListener {
	return []bot.EventListener{m.onMessageCreate, m.onMessageUpdate, m.onGippityInteractionCreate}
}

// Components returns no message-component handlers for this module.
func (m *Module) Components() []bot.ComponentHandler { return nil }

// Background returns the id/cache reset loop.
func (m *Module) Background() []bot.BackgroundTask {
	return []bot.BackgroundTask{{Name: "gippity-id-name-cache-reset", Run: m.idToNameCacheResetLoop}}
}

// Shutdown closes the chat history database.
func (m *Module) Shutdown() error {
	if m.db != nil {
		if err := m.db.Close(); err != nil {
			return fmt.Errorf("gippity: close db: %w", err)
		}
	}
	return nil
}

// DBPath returns the gippity database file path.
func (m *Module) DBPath() string { return m.dbPath }

// idToNameCacheResetLoop clears the name cache every 12 hours, or on shutdown.
func (m *Module) idToNameCacheResetLoop(ctx context.Context) {
	ticker := time.NewTicker(12 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.stateMu.Lock()
			m.idToNameCache = make(map[string]string)
			m.stateMu.Unlock()
		}
	}
}

// cachedName returns a cached id→name lookup, computing and storing it on a
// miss. compute runs without the lock held so slow Discord API lookups never
// serialize other handlers.
func (m *Module) cachedName(key string, compute func() string) string {
	m.stateMu.Lock()
	v, ok := m.idToNameCache[key]
	m.stateMu.Unlock()
	if ok && v != "" {
		return v
	}
	v = compute()
	m.stateMu.Lock()
	m.idToNameCache[key] = v
	m.stateMu.Unlock()
	return v
}

// mentionState returns the per-user mention counter and its reset time.
func (m *Module) mentionState(userID string) (int, time.Time) {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	return m.userMessageCount[userID], m.userMessageCountLastReset[userID]
}
