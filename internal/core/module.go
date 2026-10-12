package gidbig

import (
	"context"
	"log/slog"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/bot"
	"github.com/toksikk/gidbig/internal/cfg"
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
	stats     *bot.StatsRegistry
	tools     *tool.Registry
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
	m.stats = d.Stats
	m.tools = d.Tools
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
		snap := collectStatus(context.Background(), s, m.stats.All(), m.dbPaths, m.startTime)
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
	snap := collectStatus(context.Background(), s, m.stats.All(), m.dbPaths, m.startTime)
	return renderStatusText(snap, statusViewSummary, discordMessageLimit-6)
}
