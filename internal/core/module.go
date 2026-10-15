package gidbig

import (
	"context"
	"log/slog"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/bot"
	"github.com/toksikk/gidbig/internal/cfg"
	"github.com/toksikk/gidbig/internal/llm"
	"github.com/toksikk/gidbig/internal/settings"
	"github.com/toksikk/gidbig/internal/shareddb"
	"github.com/toksikk/gidbig/internal/tool"
)

// Module is the composition-root module. It owns the core Discord handlers
// (lifecycle logging, ping/pong, /status, /uptime) and publishes the core slash
// commands. It is registered with the Bot like any other module so the session
// carries a single MessageCreate and InteractionCreate handler.
type Module struct {
	session   *discordgo.Session
	conf      *cfg.Config
	llm       *llm.Client
	stats     *bot.StatsRegistry
	tools     *tool.Registry
	settings  *settings.Store
	dbPaths   []string
	startTime time.Time
}

// NewCore returns an uninitialised core module, stamping the process start time
// it reports through /status, /uptime and the bot_status tool.
func NewCore() *Module { return &Module{startTime: time.Now()} }

// Name returns the module identifier.
func (m *Module) Name() string { return "core" }

// Init wires the session, config and status registry.
func (m *Module) Init(d bot.Deps) error {
	m.session = d.Session
	m.conf = d.Config
	m.llm = d.LLM
	m.stats = d.Stats
	m.tools = d.Tools
	m.settings = d.Settings
	m.dbPaths = []string{shareddb.Filename}
	m.registerTools()
	m.registerHandlers(d.Router)
	return nil
}

// registerHandlers wires the core Discord handlers into the router. /status and
// /uptime are owner-only, gated through the router's middleware chain.
func (m *Module) registerHandlers(r *bot.Router) {
	ownerID := ""
	if m.conf != nil {
		ownerID = m.conf.Discord.OwnerID
	}
	ownerOnly := bot.OwnerOnly(ownerID)

	r.OnReady(m.onReady)
	r.OnConnect(m.onConnect)
	r.OnDisconnect(m.onDisconnect)
	r.OnResumed(m.onResumed)
	r.OnMessage(m.onMessageCreate)
	r.AddCommand(statusCommand(), m.onStatusInteractionCreate, ownerOnly)
	for _, cmd := range coreSlashCommands() {
		r.AddCommand(cmd, m.onCoreSlashInteractionCreate, ownerOnly)
	}
}

// Background returns no background tasks.
func (m *Module) Background() []bot.BackgroundTask { return nil }

// Shutdown returns nil; the composition root owns the shared database and the
// Discord session.
func (m *Module) Shutdown() error { return nil }

func (m *Module) onReady(_ *discordgo.Session, event *discordgo.Ready) {
	slog.Info("Discord READY", "user", event.User.String(), "guilds", len(event.Guilds))
}

func (m *Module) onConnect(s *discordgo.Session, _ *discordgo.Connect) {
	slog.Info("Discord WebSocket connected",
		"shard_id", s.ShardID,
		"shard_count", s.ShardCount,
		"reconnect_enabled", s.ShouldReconnectOnError,
	)
}

func (m *Module) onDisconnect(s *discordgo.Session, _ *discordgo.Disconnect) {
	// Callbacks run asynchronously and can arrive after the next Connect.
	// Do not wait for the session lock or claim this is the current state.
	slog.Warn("Discord WebSocket disconnect event received; automatic reconnect enabled",
		"shard_id", s.ShardID,
		"shard_count", s.ShardCount,
		"reconnect_enabled", s.ShouldReconnectOnError,
	)
}

func (m *Module) onResumed(_ *discordgo.Session, _ *discordgo.Resumed) {
	slog.Info("Discord session resumed")
}

// onMessageCreate handles the core ping/pong echo.
func (m *Module) onMessageCreate(s *discordgo.Session, msg *discordgo.MessageCreate) {
	if msg.Content != "ping" && msg.Content != "pong" {
		return
	}

	if msg.Content == "ping" {
		if sent, err := s.ChannelMessageSend(msg.ChannelID, "Pong!"); err != nil {
			slog.Error("could not send channel message", "message", sent, "error", err)
		}
	}
	if msg.Content == "pong" {
		if sent, err := s.ChannelMessageSend(msg.ChannelID, "Ping!"); err != nil {
			slog.Error("could not send channel message", "message", sent, "error", err)
		}
	}

	if err := s.UpdateGameStatus(0, "Ping Pong with "+msg.Author.Username); err != nil {
		slog.Error("could not set game status", "error", err)
	}
}

func (m *Module) onStatusInteractionCreate(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionApplicationCommand {
		return
	}
	data := i.ApplicationCommandData()
	if data.Name != "status" {
		return
	}

	view, format := statusOptions(data.Options)
	if view == statusViewUsers {
		respondStatusUsers(s, i, format, m.startTime)
		return
	}
	resp := statusInteractionResponse(func() *discordgo.InteractionResponseData {
		snap := m.collectSnapshot(s)
		return statusResponseData(snap, view, format)
	})
	if err := s.InteractionRespond(i.Interaction, resp); err != nil {
		slog.Error("could not respond to /status interaction", "error", err)
	}
}

func (m *Module) onCoreSlashInteractionCreate(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionApplicationCommand {
		return
	}
	if i.ApplicationCommandData().Name != "uptime" {
		return
	}
	respondEphemeral(s, i, m.buildUptimeBody())
}

// statsMessage renders the compact status used by /admin info.
func (m *Module) statsMessage(s *discordgo.Session) string {
	snap := m.collectSnapshot(s)
	return renderStatusText(snap, statusViewSummary, discordMessageLimit-6)
}

// collectSnapshot gathers the runtime status and attaches the effective LLM
// configuration, so /status and /admin info both report it.
func (m *Module) collectSnapshot(s *discordgo.Session) statusSnapshot {
	snap := collectStatus(context.Background(), s, m.stats.All(), m.dbPaths, m.startTime)
	snap.LLM = m.llmStatus()
	return snap
}

// llmStatus resolves the non-secret LLM configuration for /status. It reads the
// effective client settings from the shared llm.Client and the gippity knobs
// through the runtime settings store, so the report matches runtime behaviour —
// including a value changed with /admin config — and agrees with the gippity
// module line in the same message. Without a store the boot config applies. It
// never copies the API key or the persona text.
func (m *Module) llmStatus() llmStatus {
	st := llmStatus{AnswerMode: "mention only", ClassifierRateLimit: defaultClassifierRateLimit}
	if m.llm != nil {
		st.Info = m.llm.Info()
	}
	if m.conf == nil {
		return st
	}

	g := m.conf.Gippity
	st.ClassifierModel = m.settingString(settings.KeyGippityClassifierModel, g.ClassifierModel)
	if limit := m.settingInt(settings.KeyGippityClassifierRateLimit, g.ClassifierRateLimitPerHour); limit > 0 {
		st.ClassifierRateLimit = limit
	}
	st.MessageRateLimit = m.settingInt(settings.KeyGippityRateLimitMessagesPerHour, g.RateLimitMessagesPerHour)
	st.ToolsReasoningEffort = m.settingString(settings.KeyGippityToolsReasoningEffort, g.Tools.ReasoningEffort)
	st.ToolsEnabled = m.settingBool(settings.KeyGippityToolsEnabled, g.Tools.Enabled)
	// No floor: gippity honours a non-positive cap (it just makes no rounds), so
	// flooring it here would report a value the loop does not use.
	st.ToolsMaxIterations = m.settingInt(settings.KeyGippityToolsMaxIterations, g.Tools.MaxIterations)
	st.ToolsAllowed = m.settingStringList(settings.KeyGippityToolsAllowed, g.Tools.Allowed)
	st.DebugChannel = m.settingString(settings.KeyGippityDebugChannel, g.DebugChannel)
	if m.settingBool(settings.KeyGippityRespondToAllMessages, g.RespondToAllMessages) {
		st.AnswerMode = "all messages (classified)"
	}
	return st
}

// The four helpers below read a knob through the runtime settings store and fall
// back to the boot value when no store is wired (tests build Deps by hand).

func (m *Module) settingBool(key string, fallback bool) bool {
	if m.settings != nil {
		return m.settings.Bool(key)
	}
	return fallback
}

func (m *Module) settingInt(key string, fallback int) int {
	if m.settings != nil {
		return m.settings.Int(key)
	}
	return fallback
}

func (m *Module) settingString(key, fallback string) string {
	if m.settings != nil {
		return m.settings.String(key)
	}
	return fallback
}

func (m *Module) settingStringList(key string, fallback []string) []string {
	if m.settings != nil {
		return m.settings.StringList(key)
	}
	return fallback
}
