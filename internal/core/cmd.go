package gidbig

import (
	"bytes"
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
	"github.com/toksikk/gidbig/internal/cfg"
	"github.com/toksikk/gidbig/internal/coffee"
	"github.com/toksikk/gidbig/internal/eso"
	"github.com/toksikk/gidbig/internal/gamerstatus"
	"github.com/toksikk/gidbig/internal/gbploader"
	"github.com/toksikk/gidbig/internal/gippity"
	"github.com/toksikk/gidbig/internal/leetoclock"
	"github.com/toksikk/gidbig/internal/stoll"
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
	slog.Info("Received READY payload.")
}

func scontains(key string, options ...string) bool {
	for _, item := range options {
		if item == key {
			return true
		}
	}
	return false
}

func displayBotStats(cid string) {
	stats := runtime.MemStats{}
	runtime.ReadMemStats(&stats)

	users := 0
	for _, guild := range discord.State.Ready.Guilds {
		users += len(guild.Members)
	}

	uptime := time.Since(startTime).Round(time.Second)
	startDateTime := startTime.Format("2006-01-02 15:04:05")

	statusMessage := fmt.Sprintf(`Gidbig:          %s
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
		stats.Lookups, runtime.NumGoroutine(), len(discord.State.Ready.Guilds), users, len(*gbploader.GetLoadedPlugins()), uptime, startDateTime)

	for n, p := range *gbploader.GetLoadedPlugins() {
		statusMessage += fmt.Sprintf("%s %s\n", n, p[0])
	}

	_, err := discord.ChannelMessageSend(cid, "```"+statusMessage+"```")
	if err != nil {
		slog.Error("could not send channel message", "error", err)
	}
}

// Handles bot operator messages, should be refactored (lmao)
func handleBotControlMessages(s *discordgo.Session, m *discordgo.MessageCreate, parts []string, g *discordgo.Guild) {
	if len(parts) > 1 {
		if scontains(parts[1], "status") {
			displayBotStats(m.ChannelID)
		}
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

	msg := strings.Replace(m.ContentWithMentionsReplaced(), s.State.Ready.User.Username, "username", 1)
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
	if len(m.Mentions) > 0 && m.Author.ID == conf.Discord.OwnerID && len(parts) > 0 {
		mentioned := false
		for _, mention := range m.Mentions {
			mentioned = (mention.ID == s.State.Ready.User.ID)
			if mentioned {
				break
			}
		}

		if mentioned {
			handleBotControlMessages(s, m, parts, guild)
		}
		return
	}

	// Find the collection for the command we got
	findAndPlaySound(s, m, parts, guild)
}

func notifyOwner(message string) {
	// FIXME
	st, err := discord.UserChannelCreate(conf.Discord.OwnerID)
	if err != nil {
		return
	}
	msg, err := discord.ChannelMessageSend(st.ID, message)
	if err != nil {
		slog.Error("could not send channel message", "message", msg, "error", err)
	}
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

// StartGidbig obviously
func StartGidbig() {
	LogVersion()
	conf = cfg.GetConfig()

	// set log level to debug if env var is set
	if os.Getenv("DEBUG") != "" {
		logLevel := new(slog.LevelVar)
		logLevel.Set(slog.LevelDebug)

		logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
			Level: logLevel,
		}))

		slog.SetDefault(logger)
	}

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

	// Set sharding info
	discord.ShardID = conf.Discord.ShardID
	discord.ShardCount = conf.Discord.ShardCount

	if discord.ShardCount <= 0 {
		discord.ShardCount = 1
	}

	discord.AddHandler(onReady)
	discord.AddHandler(onMessageCreate)

	err = discord.Open()
	if err != nil {
		slog.Error("Failed to create discord websocket connection", "error", err)
		os.Exit(1)
		return
	}

	coffee.Start(discord)
	eso.Start(discord)
	gamerstatus.Start(discord)
	gippity.Start(discord)
	leetoclock.Start(discord)
	stoll.Start(discord)

	gbploader.LoadPlugins(discord)

	// We're running!
	Banner(nil, *gbploader.GetLoadedPlugins())
	slog.Info("Gidbig is ready. Quit with CTRL-C.")

	banner := new(bytes.Buffer)
	Banner(banner, *gbploader.GetLoadedPlugins())
	slog.Info("Dev Mode", "enabled", conf.DevMode)
	if conf.DevMode {
		slog.SetLogLoggerLevel(slog.LevelDebug)
		notifyOwner("```I just started!\n" + banner.String() + "```")
		setStartedStatus()
	}

	// Wait for a signal to quit
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	<-c
}
