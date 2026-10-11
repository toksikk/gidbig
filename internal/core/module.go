package gidbig

import (
	"context"
	"log/slog"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/bot"
	"github.com/toksikk/gidbig/internal/cfg"
	"github.com/toksikk/gidbig/internal/shareddb"
)

// Module is the composition-root module. It owns the core Discord handlers
// (lifecycle logging, ping/pong, /status, /uptime) and publishes the core slash
// commands. It is registered with the Bot like any other module so the session
// carries a single MessageCreate and InteractionCreate handler.
type Module struct {
	session *discordgo.Session
	conf    *cfg.Config
	stats   *bot.StatsRegistry
	dbPaths []string
}

// NewCore returns an uninitialised core module.
func NewCore() *Module { return &Module{} }

// Name returns the module identifier.
func (m *Module) Name() string { return "core" }

// Init wires the session, config and status registry.
func (m *Module) Init(d bot.Deps) error {
	m.session = d.Session
	m.conf = d.Config
	m.stats = d.Stats
	m.dbPaths = []string{shareddb.Filename}
	return nil
}

// Commands returns the core slash commands.
func (m *Module) Commands() []*discordgo.ApplicationCommand {
	cmds := []*discordgo.ApplicationCommand{statusCommand()}
	return append(cmds, coreSlashCommands()...)
}

// Listeners returns the core Discord event handlers.
func (m *Module) Listeners() []bot.EventListener {
	return []bot.EventListener{
		m.onReady,
		m.onConnect,
		m.onDisconnect,
		m.onResumed,
		m.onMessageCreate,
		m.onStatusInteractionCreate,
		m.onCoreSlashInteractionCreate,
	}
}

// Components returns no component handlers.
func (m *Module) Components() []bot.ComponentHandler { return nil }

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

	var userID string
	if i.Member != nil && i.Member.User != nil {
		userID = i.Member.User.ID
	} else if i.User != nil {
		userID = i.User.ID
	}

	view, format := statusOptions(data.Options)
	if view == statusViewUsers {
		if userID != m.conf.Discord.OwnerID {
			respondEphemeral(s, i, "Access denied.")
			return
		}
		respondStatusUsers(s, i, format)
		return
	}
	resp := statusInteractionResponse(userID, m.conf.Discord.OwnerID, func() *discordgo.InteractionResponseData {
		snap := collectStatus(context.Background(), s, m.stats.All(), m.dbPaths)
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
	user := interactionUser(i)
	if user == nil || user.ID != m.conf.Discord.OwnerID {
		respondEphemeral(s, i, "Access denied.")
		return
	}
	respondEphemeral(s, i, buildUptimeBody())
}

// statsMessage renders the compact status used by /admin info.
func (m *Module) statsMessage(s *discordgo.Session) string {
	snap := collectStatus(context.Background(), s, m.stats.All(), m.dbPaths)
	return renderStatusText(snap, statusViewSummary, discordMessageLimit-6)
}
