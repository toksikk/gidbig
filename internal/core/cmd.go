package gidbig

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/admin"
	"github.com/toksikk/gidbig/internal/anticheat"
	"github.com/toksikk/gidbig/internal/bot"
	"github.com/toksikk/gidbig/internal/cfg"
	"github.com/toksikk/gidbig/internal/coffee"
	"github.com/toksikk/gidbig/internal/eso"
	"github.com/toksikk/gidbig/internal/gamerstatus"
	"github.com/toksikk/gidbig/internal/gippity"
	"github.com/toksikk/gidbig/internal/leetoclock"
	"github.com/toksikk/gidbig/internal/llm"
	"github.com/toksikk/gidbig/internal/shareddb"
	"github.com/toksikk/gidbig/internal/soundboard"
	"github.com/toksikk/gidbig/internal/stoll"
	"github.com/toksikk/gidbig/internal/tool"
	"github.com/toksikk/gidbig/internal/wardogs"
	"github.com/toksikk/gidbig/internal/wttrin"
)

// statusInteractionResponse builds the ephemeral interaction response for /status.
// Owner gets the status payload; non-owners get a denial. build is injectable for testing.
func statusInteractionResponse(userID, ownerID string, build func() *discordgo.InteractionResponseData) *discordgo.InteractionResponse {
	if userID != ownerID {
		return &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "Access denied.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		}
	}
	return &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: build(),
	}
}

func (m *Module) setStartedStatus() {
	if m.session == nil {
		return
	}
	if err := m.session.UpdateCustomStatus(startedStatus()); err != nil {
		slog.Warn("Failed to set custom status", "error", err)
	}
}

func startedStatus() string {
	return "I just started! " + currentVersion() + " (" + builddate + ")"
}

func setupLogging(config *cfg.Config) {
	opts := &slog.HandlerOptions{}
	var logger *slog.Logger
	if config.DevMode {
		opts.Level = slog.LevelDebug
		opts.AddSource = true
		slog.Debug("Dev Mode", "devMode", config.DevMode)
		logger = slog.New(slog.NewTextHandler(os.Stdout, opts))
	} else {
		logger = slog.New(slog.NewJSONHandler(os.Stdout, opts))
	}

	slog.SetDefault(logger)
}

// newDiscordSession creates the Discord session and applies the transport and
// sharding configuration before the gateway is opened.
func newDiscordSession(conf *cfg.Config) (*discordgo.Session, error) {
	session, err := discordgo.New("Bot " + conf.Discord.Token)
	if err != nil {
		return nil, err
	}

	// The pinned discordgo fork sets no read/write deadlines on its
	// websockets, so a degraded network can hang Open() (waiting for the
	// Hello packet while holding the session lock) or the heartbeat write
	// (holding wsMutex) forever, silently stalling the reconnect loop.
	// Bound every read/write at the transport level so stalls turn into
	// errors and discordgo reconnects on its own. See wsdeadline.go.
	session.Dialer = newDeadlineDialer()

	// Capture gateway reconnect attempts and failures in the same structured
	// log stream as the application. Dev mode additionally enables payload and
	// heartbeat-level diagnostics.
	configureDiscordgoLogging(session, conf.DevMode)

	session.ShardID = conf.Discord.ShardID
	session.ShardCount = conf.Discord.ShardCount
	if session.ShardCount <= 0 {
		session.ShardCount = 1
	}
	return session, nil
}

// registerOrLog registers a module and logs (but does not fail) on error, so a
// broken module never takes the whole bot down.
func registerOrLog(b *bot.Bot, m bot.Module) bool {
	if err := b.RegisterModule(m); err != nil {
		slog.Error("module init failed", "module", m.Name(), "error", err)
		return false
	}
	return true
}

// StartGidbig obviously
func StartGidbig() {
	conf := cfg.GetConfig()
	setupLogging(conf)
	LogVersion()

	if conf.Database.Path != "" {
		slog.Warn("database.path is no longer supported and is ignored; the shared database is always "+shareddb.Filename,
			"configured_path", conf.Database.Path)
	}

	// Open the single SQLite database shared by every module. Modules receive
	// it through bot.Deps.DB instead of opening the file themselves.
	sharedDB, err := shareddb.Open(shareddb.Filename)
	if err != nil {
		slog.Error("Failed to open shared database", "error", err)
		os.Exit(1)
		return
	}
	slog.Info("Using shared database", "path", shareddb.Filename)
	warnLegacyDatabases(sharedDB)

	// Create a discord session
	slog.Info("Starting discord session...")
	session, err := newDiscordSession(conf)
	if err != nil {
		slog.Error("Failed to create discord session", "error", err)
		os.Exit(1)
		return
	}

	llmClient, err := llm.New(llm.Options{
		Provider:    conf.LLM.Provider,
		Model:       conf.LLM.Model,
		VisionModel: conf.LLM.VisionModel,
		BaseURL:     conf.LLM.BaseURL,
		HTTPReferer: conf.LLM.HTTPReferer,
		Title:       conf.LLM.Title,
		Personality: conf.LLM.Personality,
		Preset:      conf.LLM.Preset,
	})
	if err != nil {
		slog.Error("llm: init failed", "error", err)
		os.Exit(1)
		return
	}

	deps := bot.Deps{
		Session: session,
		Config:  conf,
		LLM:     llmClient,
		Logger:  slog.Default(),
		OwnerID: conf.Discord.OwnerID,
		DB:      sharedDB,
		Stats:   bot.NewStatsRegistry(),
		Tools:   tool.NewRegistry(),
	}

	b := bot.New(deps)
	// Core owns /status, /uptime and the core Discord handlers. It is
	// registered before the gateway opens so its lifecycle handlers are active
	// from the first connect.
	coreMod := NewCore()
	registerOrLog(b, coreMod)

	watchdogCtx, stopWatchdog := context.WithCancel(context.Background())
	defer stopWatchdog()
	go runDiscordWatchdog(watchdogCtx, session, discordWatchdogInterval, discordWatchdogTimeout, func() {
		os.Exit(1)
	})

	if err := session.Open(); err != nil {
		slog.Error("Failed to create discord websocket connection", "error", err)
		os.Exit(1)
		return
	}

	soundboardMod := soundboard.New()
	registerOrLog(b, soundboardMod)
	coffeeMod := coffee.New()
	coffeeReady := registerOrLog(b, coffeeMod)
	esoMod := eso.New()
	registerOrLog(b, esoMod)
	registerOrLog(b, gamerstatus.New())
	gippityMod := gippity.New()
	gippityReady := registerOrLog(b, gippityMod)

	// Admin aggregates the provider modules under /admin. Adding a new
	// AdminProvider module needs no edits inside internal/admin.
	var adminProviders []bot.AdminProvider
	if coffeeReady {
		adminProviders = append(adminProviders, coffeeMod)
	}
	if gippityReady {
		adminProviders = append(adminProviders, gippityMod)
	}
	adminMod := admin.New(adminProviders...)
	adminMod.SetInfoText(coreMod.statsMessage)
	registerOrLog(b, adminMod)

	registerOrLog(b, leetoclock.New())
	registerOrLog(b, anticheat.New())
	registerOrLog(b, stoll.New())
	registerOrLog(b, wardogs.New())
	registerOrLog(b, wttrin.New())

	// Start Webserver if a valid port is provided and if ClientID and ClientSecret are set
	if conf.Web.Port != 0 && conf.Web.Port >= 1 && conf.Web.Oauth.ClientID != "" && conf.Web.Oauth.ClientSecret != "" && conf.Web.Oauth.RedirectURI != "" {
		slog.Info("Starting web server", "port", conf.Web.Port)
		go startWebServer(conf, session, soundboardMod, esoMod)
	} else {
		slog.Info("Required web server arguments missing or invalid. Skipping web server start.")
	}

	Banner(nil)

	slog.Info("Gidbig is ready. Quit with CTRL-C.")
	coreMod.setStartedStatus()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := b.Run(ctx); err != nil {
		slog.Error("bot run failed", "error", err)
	}

	slog.Info("shutting down")
	stopWatchdog()

	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		b.Shutdown()
	}()

	select {
	case <-shutdownDone:
		slog.Info("shutdown complete")
	case <-time.After(10 * time.Second):
		slog.Warn("shutdown timed out after 10s, forcing exit")
		logGoroutineStacks()
	}

	// Close the shared database last, after modules stopped using it.
	if sqlDB, dbErr := sharedDB.DB(); dbErr == nil {
		if err := sqlDB.Close(); err != nil {
			slog.Error("error closing shared database", "error", err)
		}
	}
}
