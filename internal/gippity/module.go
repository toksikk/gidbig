package gippity

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/bot"
	"github.com/toksikk/gidbig/internal/llm"
	"github.com/toksikk/gidbig/internal/settings"
	"github.com/toksikk/gidbig/internal/tool"
	"github.com/toksikk/gidbig/internal/util"

	openai "github.com/openai/openai-go/v3"
	"gorm.io/gorm"
)

// Module implements bot.Module, bot.AdminProvider and bot.StatsProvider for the
// gippity plugin. All former package-level state now lives on this struct.
type Module struct {
	session *discordgo.Session

	db     *gorm.DB
	dbMu   sync.Mutex
	ownsDB bool

	// stateMu guards idToNameCache and the per-user mention counters, which are
	// touched by concurrent Discord handlers and the cache-reset background task.
	stateMu sync.Mutex

	idToNameCache map[string]string

	allowedGuildIDs map[string]bool
	ignoredUserIDs  map[string]bool

	userMessageCount          map[string]int
	userMessageLimit          int
	userMessageCountLastReset map[string]time.Time

	// classifierCount tracks per-user non-mention classifier calls per hour.
	classifierCount          map[string]int
	classifierCountLastReset map[string]time.Time

	// Test hooks. Production defaults are wired in New.
	generateAnswerFunc         func(*discordgo.MessageCreate, []string) (string, error)
	chatCompletionFunc         func(context.Context, openai.ChatCompletionNewParams) (*openai.ChatCompletion, error)
	channelTypingFunc          func(*discordgo.Session, string)
	channelMembersFunc         func(*discordgo.Session, string) (string, error)
	describeImagesFunc         func([]string) (string, error)
	visionCompletionFunc       func(context.Context, openai.ChatCompletionNewParams) (*openai.ChatCompletion, error)
	fetchReferencedMessageFunc func(*discordgo.Session, *discordgo.MessageReference) (*discordgo.Message, error)
	channelMessageFunc         func(*discordgo.Session, string, string) (*discordgo.Message, error)
	fetchMessageReactionsFunc  func(*discordgo.Session, string, string) (*discordgo.Message, error)
	messageSendFunc            func(*discordgo.Session, string, string) (*discordgo.Message, error)
	messageThreadStartFunc     func(*discordgo.Session, string, string, *discordgo.ThreadStart) (*discordgo.Channel, error)
	channelIsThreadFunc        func(*discordgo.Session, string) bool
	classifyFunc               func(context.Context, *discordgo.MessageCreate) (bool, error)
	seasonFunc                 func() util.Season

	// replyInThread mirrors gippity.reply_in_thread: when true, answers go into
	// a thread started on the mentioning message instead of the channel.
	replyInThread bool

	// respondToAllMessages mirrors gippity.respond_to_all_messages: when true,
	// non-mention messages from opted-in users are classified and may be
	// answered. classifierModel overrides the model for that decision;
	// classifierRateLimit caps classifier calls per user per hour;
	// classifierNames adds aliases the classifier treats as addressing the bot.
	respondToAllMessages bool
	classifierModel      string
	classifierRateLimit  int
	classifierNames      []string

	// debugChannel mirrors gippity.debug_channel: with debug logging enabled the
	// bot ignores every other channel. Empty disables the restriction.
	debugChannel string

	// tools is the shared tool registry. When toolsEnabled is true the answer
	// path offers the registered tools to the model and runs a bounded tool
	// loop; toolMaxIterations caps tool-call rounds and toolAllowed optionally
	// restricts which tools are offered.
	tools               *tool.Registry
	toolsEnabled        bool
	toolMaxIterations   int
	toolAllowed         map[string]bool
	toolReasoningEffort string

	llmClient *llm.Client

	// settings is the runtime settings store. When it is wired, every
	// runtime-tunable knob below is read through it on each use so an
	// /admin config change applies to the next message. When it is nil (tests
	// that build Deps by hand) the Init-time snapshot fields above are used.
	settings *settings.Store
}

// New returns a Module with production-default hook implementations.
func New() *Module {
	m := &Module{
		idToNameCache:            make(map[string]string),
		userMessageLimit:         defaultRateLimitPerHour,
		classifierRateLimit:      defaultClassifierRateLimit,
		classifierCount:          make(map[string]int),
		classifierCountLastReset: make(map[string]time.Time),
		toolMaxIterations:        defaultToolMaxIterations,
	}
	m.generateAnswerFunc = m.generateAnswer
	m.chatCompletionFunc = func(ctx context.Context, params openai.ChatCompletionNewParams) (*openai.ChatCompletion, error) {
		return m.llmClient.GetClient().Chat.Completions.New(ctx, params)
	}
	m.channelTypingFunc = func(s *discordgo.Session, channelID string) {
		s.ChannelTyping(channelID) //nolint:errcheck
	}
	m.channelMembersFunc = util.GetAllMembersOfChannelAsString
	m.describeImagesFunc = m.describeImages
	m.visionCompletionFunc = func(ctx context.Context, params openai.ChatCompletionNewParams) (*openai.ChatCompletion, error) {
		return m.llmClient.GetClient().Chat.Completions.New(ctx, params)
	}
	m.channelMessageFunc = func(s *discordgo.Session, channelID, messageID string) (*discordgo.Message, error) {
		return s.ChannelMessage(channelID, messageID)
	}
	m.fetchMessageReactionsFunc = func(s *discordgo.Session, channelID, messageID string) (*discordgo.Message, error) {
		return m.channelMessageFunc(s, channelID, messageID)
	}
	m.messageSendFunc = func(s *discordgo.Session, channelID, content string) (*discordgo.Message, error) {
		return s.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{Content: content, AllowedMentions: util.NoMentions()})
	}
	m.messageThreadStartFunc = func(s *discordgo.Session, channelID, messageID string, data *discordgo.ThreadStart) (*discordgo.Channel, error) {
		return s.MessageThreadStartComplex(channelID, messageID, data)
	}
	m.channelIsThreadFunc = func(s *discordgo.Session, channelID string) bool {
		if channel, err := s.State.Channel(channelID); err == nil && channel != nil {
			return channel.IsThread()
		}
		channel, err := s.Channel(channelID)
		if err != nil || channel == nil {
			return false
		}
		return channel.IsThread()
	}
	m.fetchReferencedMessageFunc = m.fetchReferencedMessage
	m.classifyFunc = m.classifyShouldRespond
	m.seasonFunc = util.CurrentSeason
	return m
}

// Name returns the module's identifier.
func (m *Module) Name() string { return "gippity" }

// Init wires dependencies and opens the chat history database.
func (m *Module) Init(d bot.Deps) error {
	m.session = d.Session
	m.llmClient = d.LLM
	m.settings = d.Settings

	if d.Config != nil && d.Config.Gippity.RateLimitMessagesPerHour > 0 {
		m.userMessageLimit = d.Config.Gippity.RateLimitMessagesPerHour
	}
	if d.Config != nil {
		m.replyInThread = d.Config.Gippity.ReplyInThread
		m.respondToAllMessages = d.Config.Gippity.RespondToAllMessages
		m.classifierModel = d.Config.Gippity.ClassifierModel
		m.classifierNames = d.Config.Gippity.ClassifierNames
		m.debugChannel = d.Config.Gippity.DebugChannel
		if d.Config.Gippity.ClassifierRateLimitPerHour > 0 {
			m.classifierRateLimit = d.Config.Gippity.ClassifierRateLimitPerHour
		}
		m.toolsEnabled = d.Config.Gippity.Tools.Enabled
		if d.Config.Gippity.Tools.MaxIterations > 0 {
			m.toolMaxIterations = d.Config.Gippity.Tools.MaxIterations
		}
		m.toolReasoningEffort = d.Config.Gippity.Tools.ReasoningEffort
		if len(d.Config.Gippity.Tools.Allowed) > 0 {
			m.toolAllowed = make(map[string]bool, len(d.Config.Gippity.Tools.Allowed))
			for _, name := range d.Config.Gippity.Tools.Allowed {
				m.toolAllowed[name] = true
			}
		}
	}

	m.tools = d.Tools
	m.registerSearchHistoryTool()
	m.registerChannelMembersTool()

	m.userMessageCount = make(map[string]int)
	m.userMessageCountLastReset = make(map[string]time.Time)
	m.classifierCount = make(map[string]int)
	m.classifierCountLastReset = make(map[string]time.Time)

	// allowedGuildIDs is the fallback when no settings store is wired; with a
	// store, guildAllowed reads gippity.allowed_guilds on every message.
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

	if err := m.openDB(d); err != nil {
		return fmt.Errorf("gippity: init db: %w", err)
	}

	d.Router.OnMessage(m.onMessageCreate)
	d.Router.OnMessageUpdate(m.onMessageUpdate)
	for _, cmd := range m.Commands() {
		d.Router.AddCommand(cmd, m.onGippityInteractionCreate)
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
					Description: "Anonymize your messages in AI context; off also opts you into chat trivia stats",
					Options: []*discordgo.ApplicationCommandOption{
						{
							Type:        discordgo.ApplicationCommandOptionString,
							Name:        "set",
							Description: "on = anonymize, no trivia (default); off = include as-is, counted in trivia",
							Required:    true,
							Choices: []*discordgo.ApplicationCommandOptionChoice{
								{Name: "on", Value: "on"},
								{Name: "off", Value: "off"},
							},
						},
					},
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "nonmention",
					Description: "Let the bot decide whether to reply to your messages that do not mention it",
					Options: []*discordgo.ApplicationCommandOption{
						{
							Type:        discordgo.ApplicationCommandOptionString,
							Name:        "set",
							Description: "on = allow non-mention replies, off = disallow (default)",
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

// Background returns the id/cache reset loop.
func (m *Module) Background() []bot.BackgroundTask {
	return []bot.BackgroundTask{
		{Name: "gippity-id-name-cache-reset", Run: m.idToNameCacheResetLoop},
		{Name: "gippity-rate-counter-prune", Run: m.rateCounterPruneLoop},
	}
}

// Shutdown closes the chat history database when this module opened it.
// An injected shared database is owned by the composition root.
func (m *Module) Shutdown() error {
	if m.db == nil || !m.ownsDB {
		return nil
	}
	sqlDB, err := m.db.DB()
	if err != nil {
		return fmt.Errorf("gippity: get sql db: %w", err)
	}
	if err := sqlDB.Close(); err != nil {
		return fmt.Errorf("gippity: close db: %w", err)
	}
	return nil
}

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

// rateCounterPruneLoop drops stale per-user rate counters every hour, so the
// counter maps do not grow with every user ever seen.
func (m *Module) rateCounterPruneLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.pruneCounters(time.Now())
		}
	}
}

// pruneCounters removes counters whose window started at least an hour ago.
// Such entries would reset on the next message anyway, so removing them does
// not change rate-limit behavior.
func (m *Module) pruneCounters(now time.Time) {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	pruneStale(m.userMessageCount, m.userMessageCountLastReset, now)
	pruneStale(m.classifierCount, m.classifierCountLastReset, now)
}

func pruneStale(counts map[string]int, lastReset map[string]time.Time, now time.Time) {
	for id, reset := range lastReset {
		if now.Sub(reset) >= time.Hour {
			delete(lastReset, id)
			delete(counts, id)
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
