package gidbig

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/admin"
	"github.com/toksikk/gidbig/internal/anticheat"
	"github.com/toksikk/gidbig/internal/bot"
	"github.com/toksikk/gidbig/internal/cfg"
	"github.com/toksikk/gidbig/internal/coffee"
	"github.com/toksikk/gidbig/internal/configcmd"
	"github.com/toksikk/gidbig/internal/eso"
	"github.com/toksikk/gidbig/internal/gamerstatus"
	"github.com/toksikk/gidbig/internal/gippity"
	"github.com/toksikk/gidbig/internal/leetoclock"
	"github.com/toksikk/gidbig/internal/llm"
	"github.com/toksikk/gidbig/internal/logscmd"
	"github.com/toksikk/gidbig/internal/logwindow"
	"github.com/toksikk/gidbig/internal/openmeteo"
	"github.com/toksikk/gidbig/internal/settings"
	"github.com/toksikk/gidbig/internal/shareddb"
	"github.com/toksikk/gidbig/internal/soundboard"
	"github.com/toksikk/gidbig/internal/stoll"
	"github.com/toksikk/gidbig/internal/tmdb"
	"github.com/toksikk/gidbig/internal/tool"
	"github.com/toksikk/gidbig/internal/tv"
	"github.com/toksikk/gidbig/internal/wardogs"
	"github.com/toksikk/gidbig/internal/wttrin"
)

// statusInteractionResponse wraps a status payload in the /status interaction
// response. Access control is the OwnerOnly middleware on the command.
func statusInteractionResponse(build func() *discordgo.InteractionResponseData) *discordgo.InteractionResponse {
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

// logLevel is the process-wide slog level. It is the only part of dev_mode that
// follows a runtime change; the handler format and AddSource are fixed at boot.
var logLevel slog.LevelVar

// applyDevModeLevel sets lv to Debug in dev mode and to Info otherwise.
func applyDevModeLevel(lv *slog.LevelVar, devMode bool) {
	if devMode {
		lv.Set(slog.LevelDebug)
		return
	}
	lv.Set(slog.LevelInfo)
}

// wireDevModeLevel applies the effective dev_mode from the settings store to lv
// and keeps it in sync with every later /admin config change. mu serialises the
// read-and-set so two concurrent writes cannot apply their values out of order;
// it is separate from the store lock, which is released before callbacks run.
func wireDevModeLevel(store *settings.Store, lv *slog.LevelVar) {
	var mu sync.Mutex
	apply := func(msg string) {
		mu.Lock()
		defer mu.Unlock()
		before := lv.Level()
		devMode := store.Bool(settings.KeyDevMode)
		applyDevModeLevel(lv, devMode)
		if lv.Level() != before {
			slog.Info(msg, "dev_mode", devMode, "level", lv.Level().String())
		}
	}
	apply("log level applied from stored dev_mode")
	store.OnChange(settings.KeyDevMode, func() { apply("log level updated from dev_mode") })
}

// setupLogging installs the process logger and returns the in-memory log
// window that tees every record it accepts, for /admin logs show and the
// /admin/logs page.
func setupLogging(config *cfg.Config) *logwindow.Window {
	window := logwindow.New(config.LogWindowLines())
	opts := &slog.HandlerOptions{Level: &logLevel}
	applyDevModeLevel(&logLevel, config.DevMode)
	var logger *slog.Logger
	if config.DevMode {
		opts.AddSource = true
		slog.Debug("Dev Mode", "devMode", config.DevMode)
		logger = slog.New(window.Handler(slog.NewTextHandler(os.Stdout, opts)))
	} else {
		logger = slog.New(window.Handler(slog.NewJSONHandler(os.Stdout, opts)))
	}

	slog.SetDefault(logger)
	return window
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

// registerTMDB registers the optional tmdb module only when a read access token
// is configured, so a deployment without TMDB gets no command, no tool and an
// info line instead of an init error.
func registerTMDB(b *bot.Bot, conf *cfg.Config) bool {
	if conf.TMDB.ReadAccessToken == "" {
		slog.Info("tmdb: no read_access_token, module disabled")
		return false
	}
	return registerOrLog(b, tmdb.New())
}

// StartGidbig obviously
func StartGidbig() {
	processStart := time.Now()
	conf := cfg.GetConfig()
	logWindow := setupLogging(conf)
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

	// The runtime settings store layers /admin config overrides on top of the
	// loaded config. It is built before the llm client so the client can resolve
	// llm.model per request and pick up a runtime change without a restart.
	settingsStore, err := settings.New(sharedDB, conf)
	if err != nil {
		slog.Error("Failed to open runtime settings store", "error", err)
		os.Exit(1)
		return
	}
	// A stored dev_mode override wins over config.yaml for the log level.
	wireDevModeLevel(settingsStore, &logLevel)

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
		APIKey:      conf.LLM.APIKey,
		Personality: conf.LLM.Personality,
		Preset:      conf.LLM.Preset,
		MaxTokens:   int64(conf.LLM.MaxTokens),
		// The model and the completion cap are resolved per request so
		// /admin config set llm.model / llm.max_tokens applies to the next
		// answer instead of needing a restart.
		ModelOverride:       func() (string, bool) { return settingsStore.Override(settings.KeyLLMModel) },
		VisionModelOverride: func() (string, bool) { return settingsStore.Override(settings.KeyLLMVisionModel) },
		MaxTokensOverride:   func() (int, bool) { return settingsStore.OverrideInt(settings.KeyLLMMaxTokens) },
		// The persona is resolved per request too, so /admin config set
		// llm.personality / llm.personality_preset applies without a restart.
		PersonalityOverride: func() (string, bool) { return settingsStore.Override(settings.KeyLLMPersonality) },
		PresetOverride:      func() (string, bool) { return settingsStore.Override(settings.KeyLLMPersonalityPreset) },
	})
	if err != nil {
		slog.Error("llm: init failed", "error", err)
		os.Exit(1)
		return
	}

	deps := bot.Deps{
		Session:  session,
		Config:   conf,
		LLM:      llmClient,
		Logger:   slog.Default(),
		OwnerID:  conf.Discord.OwnerID,
		DB:       sharedDB,
		Stats:    bot.NewStatsRegistry(),
		Tools:    tool.NewRegistry(),
		Settings: settingsStore,
	}

	b := bot.New(deps)
	// Core owns /status, /uptime and the core Discord handlers. It is
	// registered before the gateway opens so its lifecycle handlers are active
	// from the first connect.
	coreMod := NewCore()
	// Uptime (/status, /uptime, bot_status) is measured from process start, not
	// from when the core module happens to be constructed after startup work.
	coreMod.startTime = processStart
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

	// configcmd owns the /admin config group. It is registered as a module so
	// its Init runs, and passed to admin.New below so the group appears; without
	// both it would be dead code.
	configMod := configcmd.New()
	configReady := registerOrLog(b, configMod)
	// logscmd owns the /admin logs group; like configcmd it needs both the
	// registration and the admin.New entry below.
	logsMod := logscmd.New(logWindow)
	logsReady := registerOrLog(b, logsMod)

	// Admin aggregates the provider modules under /admin. Adding a new
	// AdminProvider module needs no edits inside internal/admin.
	var adminProviders []bot.AdminProvider
	if coffeeReady {
		adminProviders = append(adminProviders, coffeeMod)
	}
	if gippityReady {
		adminProviders = append(adminProviders, gippityMod)
	}
	if configReady {
		adminProviders = append(adminProviders, configMod)
	}
	if logsReady {
		adminProviders = append(adminProviders, logsMod)
	}
	adminMod := admin.New(adminProviders...)
	adminMod.SetInfoText(coreMod.statsMessage)
	registerOrLog(b, adminMod)

	registerOrLog(b, leetoclock.New())
	registerOrLog(b, anticheat.New())
	registerOrLog(b, stoll.New())
	registerOrLog(b, wardogs.New())
	registerOrLog(b, wttrin.New())
	registerOrLog(b, openmeteo.New())
	registerOrLog(b, tv.New())
	registerTMDB(b, conf)

	// Start Webserver if a valid port is provided and if ClientID and ClientSecret are set
	if conf.Web.Port != 0 && conf.Web.Port >= 1 && conf.Web.Oauth.ClientID != "" && conf.Web.Oauth.ClientSecret != "" && conf.Web.Oauth.RedirectURI != "" {
		slog.Info("Starting web server", "port", conf.Web.Port)
		// configMod is passed even when it failed to register: the panel then
		// reports the missing store instead of hiding the reason behind a 404.
		go startWebServer(conf, session, soundboardMod, esoMod, configMod, logWindow)
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
