package gidbig

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/bwmarrin/discordgo"
	humanize "github.com/dustin/go-humanize"
	"github.com/toksikk/gidbig/internal/admin"
	"github.com/toksikk/gidbig/internal/bot"
	"github.com/toksikk/gidbig/internal/cfg"
	"github.com/toksikk/gidbig/internal/coffee"
	"github.com/toksikk/gidbig/internal/eso"
	"github.com/toksikk/gidbig/internal/gamerstatus"
	"github.com/toksikk/gidbig/internal/gbploader"
	"github.com/toksikk/gidbig/internal/gippity"
	"github.com/toksikk/gidbig/internal/leetoclock"
	"github.com/toksikk/gidbig/internal/llm"
	"github.com/toksikk/gidbig/internal/stoll"
	"github.com/toksikk/gidbig/internal/wttrin"
)

var (
	// discordgo session
	discord *discordgo.Session

	// Config struct to pass around
	conf *cfg.Config

	// mutex for checking if voice connection already exists
	mutex = &sync.Mutex{}

	// Start time for uptime calculation
	startTime = time.Now()
)

func onReady(s *discordgo.Session, event *discordgo.Ready) {
	slog.Info("Discord READY", "session_id", event.SessionID, "user", event.User.String(), "guilds", len(event.Guilds), "latency_ms", s.HeartbeatLatency().Milliseconds())
}

func onConnect(s *discordgo.Session, event *discordgo.Connect) {
	slog.Info("Discord WebSocket connected")
}

func onDisconnect(s *discordgo.Session, event *discordgo.Disconnect) {
	slog.Error("Discord WebSocket disconnected — bot is offline until reconnect")
}

func onResumed(s *discordgo.Session, event *discordgo.Resumed) {
	slog.Info("Discord session resumed", "latency_ms", s.HeartbeatLatency().Milliseconds())
}

func scontains(key string, options ...string) bool {
	for _, item := range options {
		if item == key {
			return true
		}
	}
	return false
}

func displayUptime(channelid string) {
	uptime := time.Since(startTime).Round(time.Second)
	startDateTime := startTime.Format("2006-01-02 15:04:05")
	uptimeMessage := fmt.Sprintf("`Uptime: %s (since %s)`", uptime, startDateTime)
	if _, err := discord.ChannelMessageSend(channelid, uptimeMessage); err != nil {
		slog.Error("could not send channel message", "error", err)
	}
}

func buildBotStatsMessage(s *discordgo.Session) string {
	stats := runtime.MemStats{}
	runtime.ReadMemStats(&stats)

	users := 0
	servers := 0
	if s.State != nil {
		servers = len(s.State.Guilds)
		for _, guild := range s.State.Guilds {
			users += len(guild.Members)
		}
	}

	uptime := time.Since(startTime).Round(time.Second)
	startDateTime := startTime.Format("2006-01-02 15:04:05")

	msg := fmt.Sprintf(`Gidbig:          %s
Discordgo:       %s
Go:              %s

Memory:
  Alloc:         %s
  Sys:           %s
  TotalAlloc:    %s

Live Memory Objects:
  Malloc:        %s
  Frees:         %s

Heap:
  Alloc:         %s
  InUse:         %s
  Sys:           %s

Heap Returnable:
  HeapIdle:      %s
  HeapReleased:  %s

Stack:
  InUse:         %s
  Sys:           %s

Pointer Lookups: %d
Tasks:           %d
Servers:         %d
Users:           %d
Plugins:         %d

Uptime:          %s (since %s)

Loaded Plugins:
`, version, discordgo.VERSION, runtime.Version(),
		humanize.Bytes(stats.Alloc), humanize.Bytes(stats.Sys), humanize.Bytes(stats.TotalAlloc),
		humanize.Bytes(stats.Mallocs), humanize.Bytes(stats.Frees),
		humanize.Bytes(stats.HeapAlloc), humanize.Bytes(stats.HeapInuse), humanize.Bytes(stats.HeapSys),
		humanize.Bytes(stats.HeapIdle), humanize.Bytes(stats.HeapReleased),
		humanize.Bytes(stats.StackInuse), humanize.Bytes(stats.StackSys),
		stats.Lookups, runtime.NumGoroutine(), servers, users, len(*gbploader.GetLoadedPlugins()), uptime, startDateTime)

	var sb strings.Builder
	sb.WriteString(msg)
	for n, p := range *gbploader.GetLoadedPlugins() {
		fmt.Fprintf(&sb, "%s %s\n", n, p[0])
	}

	return sb.String()
}

// statusInteractionResponse builds the ephemeral interaction response for /status.
// Owner gets the stats block; non-owners get a denial. buildStats is injectable for testing.
func statusInteractionResponse(userID, ownerID string, buildStats func() string) *discordgo.InteractionResponse {
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
		Data: &discordgo.InteractionResponseData{
			Content: "```" + buildStats() + "```",
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	}
}

func onStatusInteractionCreate(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionApplicationCommand {
		return
	}
	if i.ApplicationCommandData().Name != "status" {
		return
	}

	var userID string
	if i.Member != nil {
		userID = i.Member.User.ID
	} else if i.User != nil {
		userID = i.User.ID
	}

	resp := statusInteractionResponse(userID, conf.Discord.OwnerID, func() string {
		return buildBotStatsMessage(s)
	})
	if err := s.InteractionRespond(i.Interaction, resp); err != nil {
		slog.Error("could not respond to /status interaction", "error", err)
	}
}

func onMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Content == "ping" || m.Content == "pong" {
		// If the message is "ping" reply with "Pong!"
		if m.Content == "ping" {
			msg, err := s.ChannelMessageSend(m.ChannelID, "Pong!")
			if err != nil {
				slog.Error("could not send channel message", "message", msg, "error", err)
			}
		}

		// If the message is "pong" reply with "Ping!"
		if m.Content == "pong" {
			msg, err := s.ChannelMessageSend(m.ChannelID, "Ping!")
			if err != nil {
				slog.Error("could not send channel message", "message", msg, "error", err)
			}
		}

		// Updating bot status
		err := s.UpdateGameStatus(0, "Ping Pong with "+m.Author.Username)
		if err != nil {
			slog.Error("could not set game status", "error", err)
		}
	}
	if len(m.Content) <= 0 || (m.Content[0] != '!' && len(m.Mentions) < 1) {
		return
	}

	if m.Content == "!list" {
		var list string
		for _, c := range COLLECTIONS {
			list += "**!" + c.Prefix + "**\n"
			for _, sounds := range c.Sounds {
				list += sounds.Name + "\n"
			}
			list += "\n"
		}
		st, _ := s.UserChannelCreate(m.Author.ID)
		msg, err := s.ChannelMessageSend(st.ID, list)
		if err != nil {
			slog.Error("could not send channel message", "message", msg, "error", err)
		}
		go deleteCommandMessage(s, m.ChannelID, m.ID)
	}

	msg := strings.Replace(m.ContentWithMentionsReplaced(), s.State.User.Username, "username", 1)
	parts := strings.Split(strings.ToLower(msg), " ")

	channel, _ := discord.State.Channel(m.ChannelID)
	if channel == nil {
		slog.Warn("Failed to grab channel", "channel", m.ChannelID, "message", m.ID)
		return
	}

	guild, _ := discord.State.Guild(channel.GuildID)
	if guild == nil {
		slog.Warn("Failed to grab guild", "guild", channel.GuildID, "channel", channel, "message", m.ID)
		return
	}

	// If this is a mention, it should come from the owner (otherwise we don't care)
	if m.Author.ID == conf.Discord.OwnerID && len(parts) > 0 {
		if len(parts) == 1 {
			if scontains(parts[0], "!uptime") {
				displayUptime(m.ChannelID)
			}
		}
	}

	// Find the collection for the command we got
	findAndPlaySound(s, m, parts, guild)
}

func setStartedStatus() {
	err := discord.UpdateCustomStatus("I just started! " + version + " (" + builddate + ")")
	if err != nil {
		slog.Warn("Failed to set custom status", "error", err)
	}
}

// Delete the message after a delay so the channel does not get cluttered
func deleteCommandMessage(s *discordgo.Session, channelID string, messageID string) {
	time.Sleep(30 * time.Second)
	err := s.ChannelMessageDelete(channelID, messageID)
	if err != nil {
		slog.Error("Failed to delete message", "error", err)
	}
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

// StartGidbig obviously
func StartGidbig() {
	conf = cfg.GetConfig()
	setupLogging(conf)
	LogVersion()
	var err error

	// create SoundCollections by scanning the audio folder
	createCollections()

	// Start Webserver if a valid port is provided and if ClientID and ClientSecret are set
	if conf.Web.Port != 0 && conf.Web.Port >= 1 && conf.Web.Oauth.ClientID != "" && conf.Web.Oauth.ClientSecret != "" && conf.Web.Oauth.RedirectURI != "" {
		slog.Info("Starting web server", "port", conf.Web.Port)
		go startWebServer(conf)
	} else {
		slog.Info("Required web server arguments missing or invalid. Skipping web server start.")
	}

	// Preload all the sounds
	slog.Info("Preloading sounds...")
	for _, coll := range COLLECTIONS {
		coll.Load()
	}

	// Create a discord session
	slog.Info("Starting discord session...")
	discord, err = discordgo.New("Bot " + conf.Discord.Token)
	if err != nil {
		slog.Error("Failed to create discord session", "error", err)
		os.Exit(1)
		return
	}

	// Surface the discordgo voice-protocol logs so #113 can be diagnosed from
	// production: encryption mode negotiated, DAVE handshake progress, UDP
	// errors, etc. Only on dev mode — LogDebug is very verbose.
	if conf.DevMode {
		discord.LogLevel = discordgo.LogDebug
	}

	// Set sharding info
	discord.ShardID = conf.Discord.ShardID
	discord.ShardCount = conf.Discord.ShardCount

	if discord.ShardCount <= 0 {
		discord.ShardCount = 1
	}

	discord.AddHandler(onReady)
	discord.AddHandler(onConnect)
	discord.AddHandler(onDisconnect)
	discord.AddHandler(onResumed)
	discord.AddHandler(onMessageCreate)
	discord.AddHandler(onStatusInteractionCreate)

	err = discord.Open()
	if err != nil {
		slog.Error("Failed to create discord websocket connection", "error", err)
		os.Exit(1)
		return
	}

	llm.Initialize()
	coffeeMod := coffee.New()
	if err := coffeeMod.Init(bot.Deps{Session: discord, Config: conf}); err != nil {
		slog.Error("coffee: init failed", "error", err)
	} else {
		for _, l := range coffeeMod.Listeners() {
			discord.AddHandler(l)
		}
	}
	admin.RegisterProvider(coffeeMod)
	admin.Start(discord, conf.Discord.OwnerID, buildBotStatsMessage)
	esoMod := eso.New()
	if err := esoMod.Init(bot.Deps{Session: discord, OwnerID: conf.Discord.OwnerID}); err != nil {
		slog.Error("eso: init failed", "error", err)
	} else {
		for _, l := range esoMod.Listeners() {
			discord.AddHandler(l)
		}
	}
	bgCtx, bgCancel := context.WithCancel(context.Background())
	bgSupervisor := bot.NewSupervisor()
	gamerstatusMod := gamerstatus.New()
	if err := gamerstatusMod.Init(bot.Deps{Session: discord, OwnerID: conf.Discord.OwnerID}); err != nil {
		slog.Error("gamerstatus: init failed", "error", err)
	} else {
		bgSupervisor.Start(bgCtx, gamerstatusMod.Background()...)
	}
	gippity.Start(discord)
	leetoclock.Start(discord)
	stollMod := stoll.New()
	if err := stollMod.Init(bot.Deps{Session: discord, OwnerID: conf.Discord.OwnerID}); err != nil {
		slog.Error("stoll: init failed", "error", err)
	} else {
		for _, l := range stollMod.Listeners() {
			discord.AddHandler(l)
		}
	}
	wttrinMod := wttrin.New()
	if err := wttrinMod.Init(bot.Deps{Session: discord, OwnerID: conf.Discord.OwnerID, LLM: llm.GetClient()}); err != nil {
		slog.Error("wttrin: init failed", "error", err)
	} else {
		for _, l := range wttrinMod.Listeners() {
			discord.AddHandler(l)
		}
	}

	cmds := []*discordgo.ApplicationCommand{
		{Name: "status", Description: "Show bot runtime status (owner only)"},
	}
	cmds = append(cmds, admin.Commands()...)
	cmds = append(cmds, coffeeMod.Commands()...)
	cmds = append(cmds, esoMod.Commands()...)
	cmds = append(cmds, gippity.Commands()...)
	cmds = append(cmds, stollMod.Commands()...)
	if _, err := discord.ApplicationCommandBulkOverwrite(discord.State.User.ID, "", cmds); err != nil {
		slog.Error("Failed to register slash commands", "error", err)
	}

	gbploader.LoadPlugins(discord)

	Banner(nil, *gbploader.GetLoadedPlugins())
	Banner(new(bytes.Buffer), *gbploader.GetLoadedPlugins())

	slog.Info("Gidbig is ready. Quit with CTRL-C.")
	setStartedStatus()

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	<-c

	slog.Info("shutting down")

	bgCancel()
	bgSupervisor.Wait()

	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		if err := discord.Close(); err != nil {
			slog.Error("error closing discord session", "error", err)
		}
		gippity.Shutdown()
		gippity.CloseDB()
		leetoclock.Shutdown()
		if err := coffeeMod.Shutdown(); err != nil {
			slog.Error("coffee: shutdown failed", "error", err)
		}
	}()

	select {
	case <-shutdownDone:
		slog.Info("shutdown complete")
	case <-time.After(10 * time.Second):
		slog.Warn("shutdown timed out after 10s, forcing exit")
	}
}
