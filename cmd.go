package gidbig

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"io/ioutil"
	"net/url"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/bwmarrin/discordgo"
	humanize "github.com/dustin/go-humanize"
	log "github.com/sirupsen/logrus"
	"github.com/toksikk/gidbig/pkg/cfg"
	"github.com/toksikk/gidbig/pkg/status"
	"github.com/toksikk/gidbig/pkg/util"
	"github.com/toksikk/gidbig/pkg/wttrin"
)

var (
	// discordgo session
	discord *discordgo.Session

	// Config struct to pass around
	conf cfg.Config

	// mutex for checking if voice connection already exists
	mutex = &sync.Mutex{}
)

var (
	// Map of Guild id's to *Play channels, used for queuing and rate-limiting guilds
	queues = make(map[string]chan *Play)

	// bitrate Sound encoding settings
	bitrate = 128
	// maxQueueSize Sound encoding settings
	maxQueueSize = 6
)

// COLLECTIONS all collections
var COLLECTIONS []*soundCollection

// Create collections
func createCollections() {
	files, _ := ioutil.ReadDir("./audio")
	for _, f := range files {
		if strings.Contains(f.Name(), ".dca") {
			soundfile := strings.Split(strings.Replace(f.Name(), ".dca", "", -1), "_")
			containsPrefix := false
			containsSound := false

			if len(COLLECTIONS) == 0 {
				addNewSoundCollection(soundfile[0], soundfile[1])
			}
			for _, c := range COLLECTIONS {
				if c.Prefix == soundfile[0] {
					containsPrefix = true
					for _, sound := range c.Sounds {
						if sound.Name == soundfile[1] {
							containsSound = true
						}
					}
					if !containsSound {
						c.Sounds = append(c.Sounds, createSound(soundfile[1], 1, 250))
					}
				}
			}
			if !containsPrefix {
				addNewSoundCollection(soundfile[0], soundfile[1])
			}
		}
	}
}

func addNewSoundCollection(prefix string, soundname string) {
	var SC = &soundCollection{
		Prefix: prefix,
		Commands: []string{
			"!" + prefix,
		},
		Sounds: []*soundClip{
			createSound(soundname, 1, 250),
		},
	}
	COLLECTIONS = append(COLLECTIONS, SC)
}

// Create a Sound struct
func createSound(Name string, Weight int, PartDelay int) *soundClip {
	return &soundClip{
		Name:      Name,
		Weight:    Weight,
		PartDelay: PartDelay,
		buffer:    make([][]byte, 0),
	}
}

// Load soundcollection
func (sc *soundCollection) Load() {
	for _, sound := range sc.Sounds {
		sc.soundRange += sound.Weight
		sound.Load(sc)
	}
}

// Random select sound
func (sc *soundCollection) Random() *soundClip {
	var (
		i      int
		number = util.RandomRange(0, sc.soundRange)
	)

	for _, sound := range sc.Sounds {
		i += sound.Weight

		if number < i {
			return sound
		}
	}
	return nil
}

// Load attempts to load an encoded sound file from disk
// DCA files are pre-computed sound files that are easy to send to Discord.
// If you would like to create your own DCA files, please use:
// https://github.com/nstafie/dca-rs
// eg: dca-rs --raw -i <input wav file> > <output file>
func (s *soundClip) Load(c *soundCollection) error {
	path := fmt.Sprintf("audio/%v_%v.dca", c.Prefix, s.Name)

	file, err := os.Open(path)

	if err != nil {
		log.Error("error opening dca file :", err)
		return err
	}

	var opuslen int16

	for {
		// read opus frame length from dca file
		err = binary.Read(file, binary.LittleEndian, &opuslen)

		// If this is the end of the file, just return
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return nil
		}

		if err != nil {
			log.Error("error reading from dca file :", err)
			return err
		}

		// read encoded pcm from dca file
		InBuf := make([]byte, opuslen)
		err = binary.Read(file, binary.LittleEndian, &InBuf)

		// Should not be any end of file errors
		if err != nil {
			log.Error("error reading from dca file :", err)
			return err
		}

		// append encoded pcm data to the buffer
		s.buffer = append(s.buffer, InBuf)
	}
}

// Play plays this sound over the specified VoiceConnection
func (s *soundClip) Play(vc *discordgo.VoiceConnection) {
	vc.Speaking(true)
	defer vc.Speaking(false)

	for _, buff := range s.buffer {
		vc.OpusSend <- buff
	}
}

// Attempts to find the current users voice channel inside a given guild
func getCurrentVoiceChannel(user *discordgo.User, guild *discordgo.Guild) *discordgo.Channel {
	for _, vs := range guild.VoiceStates {
		if vs.UserID == user.ID {
			channel, _ := discord.State.Channel(vs.ChannelID)
			return channel
		}
	}
	return nil
}

// Prepares a play
func createPlay(user *discordgo.User, guild *discordgo.Guild, coll *soundCollection, sound *soundClip) *Play {
	// Grab the users voice channel
	channel := getCurrentVoiceChannel(user, guild)
	if channel == nil {
		log.WithFields(log.Fields{
			"user":  user.ID,
			"guild": guild.ID,
		}).Warning("Failed to find channel to play sound in")
		return nil
	}

	// Create the play
	play := &Play{
		GuildID:   guild.ID,
		ChannelID: channel.ID,
		UserID:    user.ID,
		Sound:     sound,
		Forced:    true,
	}

	// If we didn't get passed a manual sound, generate a random one
	if play.Sound == nil {
		play.Sound = coll.Random()
		play.Forced = false
	}

	// If the collection is a chained one, set the next sound
	if coll.ChainWith != nil {
		play.Next = &Play{
			GuildID:   play.GuildID,
			ChannelID: play.ChannelID,
			UserID:    play.UserID,
			Sound:     coll.ChainWith.Random(),
			Forced:    play.Forced,
		}
	}

	return play
}

// Prepares and enqueues a play into the ratelimit/buffer guild queue
func enqueuePlay(user *discordgo.User, guild *discordgo.Guild, coll *soundCollection, sound *soundClip) {
	play := createPlay(user, guild, coll, sound)
	if play == nil {
		return
	}
	if sound != nil {
		log.WithFields(log.Fields{
			"user": user,
		}).Info(user.Username + " triggered sound playback of !" + coll.Prefix + " " + sound.Name + " for server " + guild.Name + " in channel " + play.ChannelID)
	} else {
		log.WithFields(log.Fields{
			"user": user,
		}).Info(user.Username + " triggered sound playback of !" + coll.Prefix + " for server " + guild.Name + " in channel " + play.ChannelID)
	}
	// Check if we already have a connection to this guild
	// this should be threadsafe
	mutex.Lock()
	_, exists := queues[guild.ID]
	mutex.Unlock()

	if exists {
		if len(queues[guild.ID]) < maxQueueSize {
			mutex.Lock()
			queues[guild.ID] <- play
			mutex.Unlock()
		}
	} else {
		mutex.Lock()
		queues[guild.ID] = make(chan *Play, maxQueueSize)
		mutex.Unlock()
		playSound(play, nil)
	}
}

// Play a sound
func playSound(play *Play, vc *discordgo.VoiceConnection) (err error) {
	log.WithFields(log.Fields{
		"play": play,
	}).Info("Playing sound")

	if vc != nil {
		if vc.GuildID != play.GuildID {
			vc.Disconnect()
			vc = nil
		}
	}

	if vc == nil {
		vc, err = discord.ChannelVoiceJoin(play.GuildID, play.ChannelID, false, true)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err,
			}).Error("Failed to play sound")
			mutex.Lock()
			delete(queues, play.GuildID)
			mutex.Unlock()
			return err
		}
	}

	// If we need to change channels, do that now
	if vc.ChannelID != play.ChannelID {
		vc.ChangeChannel(play.ChannelID, false, true)
		time.Sleep(time.Millisecond * 125)
	}

	// Sleep for a specified amount of time before playing the sound
	time.Sleep(time.Millisecond * 32)

	// Play the sound
	play.Sound.Play(vc)

	// If this is chained, play the chained sound
	if play.Next != nil {
		playSound(play.Next, vc)
	}

	// If there is another song in the queue, recurse and play that
	if len(queues[play.GuildID]) > 0 {
		play = <-queues[play.GuildID]
		playSound(play, vc)
		return nil
	}

	// If the queue is empty, delete it
	time.Sleep(time.Millisecond * time.Duration(play.Sound.PartDelay))
	mutex.Lock()
	delete(queues, play.GuildID)
	vc.Disconnect()
	mutex.Unlock()
	return nil
}

func onReady(s *discordgo.Session, event *discordgo.Ready) {
	log.Info("Received READY payload.")
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

	w := &tabwriter.Writer{}
	buf := &bytes.Buffer{}

	w.Init(buf, 0, 4, 0, ' ', 0)
	fmt.Fprintf(w, "```\n")
	fmt.Fprintf(w, "Gidbig: \t%s\n", Version)
	fmt.Fprintf(w, "Discordgo: \t%s\n", discordgo.VERSION)
	fmt.Fprintf(w, "Go: \t%s\n", runtime.Version())
	fmt.Fprintf(w, "Memory: \t%s / %s (%s total allocated)\n", humanize.Bytes(stats.Alloc), humanize.Bytes(stats.Sys), humanize.Bytes(stats.TotalAlloc))
	fmt.Fprintf(w, "Tasks: \t%d\n", runtime.NumGoroutine())
	fmt.Fprintf(w, "Servers: \t%d\n", len(discord.State.Ready.Guilds))
	fmt.Fprintf(w, "Users: \t%d\n", users)
	fmt.Fprintf(w, "```\n")
	w.Flush()
	discord.ChannelMessageSend(cid, buf.String())
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
			s.ChannelMessageSend(m.ChannelID, "Pong!")
		}

		// If the message is "pong" reply with "Ping!"
		if m.Content == "pong" {
			s.ChannelMessageSend(m.ChannelID, "Ping!")
		}

		// Updating bot status
		s.UpdateGameStatus(0, "Ping Pong with "+m.Author.Username)
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
		s.ChannelMessageSend(st.ID, list)
		go deleteCommandMessage(s, m.ChannelID, m.ID)
	}

	msg := strings.Replace(m.ContentWithMentionsReplaced(), s.State.Ready.User.Username, "username", 1)
	parts := strings.Split(strings.ToLower(msg), " ")

	channel, _ := discord.State.Channel(m.ChannelID)
	if channel == nil {
		log.WithFields(log.Fields{
			"channel": m.ChannelID,
			"message": m.ID,
		}).Warning("Failed to grab channel")
		return
	}

	guild, _ := discord.State.Guild(channel.GuildID)
	if guild == nil {
		log.WithFields(log.Fields{
			"guild":   channel.GuildID,
			"channel": channel,
			"message": m.ID,
		}).Warning("Failed to grab guild")
		return
	}

	// If this is a mention, it should come from the owner (otherwise we don't care)
	if len(m.Mentions) > 0 && m.Author.ID == conf.Owner && len(parts) > 0 {
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

	if parts[0] == "!wttr" || strings.Contains(parts[0], "!wttrp") {
		handleWttrQuery(s, m, parts, guild)
	}

	// Find the collection for the command we got
	findAndPlaySound(s, m, parts, guild)
}

func notifyOwner(message string) {
	// FIXME
	st, err := discord.UserChannelCreate(conf.Owner)
	if err != nil {
		return
	}
	discord.ChannelMessageSend(st.ID, message)
}

func findSoundAndCollection(command string, soundname string) (*soundClip, *soundCollection) {
	for _, c := range COLLECTIONS {
		if scontains(command, c.Commands...) {
			for _, s := range c.Sounds {
				if soundname == s.Name {
					return s, c
				}
			}
			return nil, c
		}
	}
	return nil, nil
}

func handleWttrQuery(s *discordgo.Session, m *discordgo.MessageCreate, parts []string, g *discordgo.Guild) {
	if len(parts) > 1 {
		query := strings.Split(strings.Join(parts[1:], ""), "?")
		switch parts[0] {
		case "!wttr":
			wttr, err := wttrin.Weather(query[0] + "?format=4")
			if err != nil {
				log.WithFields(log.Fields{
					"error": err,
				}).Error("wttr.in query failed")
				s.ChannelMessageSend(m.ChannelID, err.Error())
				return
			}
			s.ChannelMessageSend(m.ChannelID, string(wttr))
		case "!wttrp":
			var wttr []byte
			var err error
			if len(query) > 1 {
				wttr, err = wttrin.Weather(url.QueryEscape(query[0]) + ".png" + "?" + query[1])
			} else {
				wttr, err = wttrin.Weather(url.QueryEscape(query[0]) + ".png?0")
			}
			if err != nil {
				log.WithFields(log.Fields{
					"error": err,
				}).Error("wttr.in query failed")
				s.ChannelMessageSend(m.ChannelID, err.Error())
				return
			}
			s.ChannelFileSend(m.ChannelID, strings.Join(parts, "")+".png", bytes.NewReader(wttr))
		case "!wttrp2":
			var wttr []byte
			var err error
			if len(query) > 1 {
				wttr, err = wttrin.WeatherV2(url.QueryEscape(query[0]) + ".png" + "?" + query[1])
			} else {
				wttr, err = wttrin.WeatherV2(url.QueryEscape(query[0]) + ".png")
			}
			if err != nil {
				log.WithFields(log.Fields{
					"error": err,
				}).Error("wttr.in query failed")
				s.ChannelMessageSend(m.ChannelID, err.Error())
				return
			}
			s.ChannelFileSend(m.ChannelID, strings.Join(parts, "")+".png", bytes.NewReader(wttr))
		}

	}
}

// Find sound in collection and play it or do nothing if not found
func findAndPlaySound(s *discordgo.Session, m *discordgo.MessageCreate, parts []string, g *discordgo.Guild) {
	for _, coll := range COLLECTIONS {
		if scontains(parts[0], coll.Commands...) {
			go deleteCommandMessage(s, m.ChannelID, m.ID)

			// If they passed a specific sound effect, find and select that (otherwise play nothing)
			var sound *soundClip
			if len(parts) > 1 {
				for _, s := range coll.Sounds {
					if parts[1] == s.Name {
						sound = s
					}
				}

				if sound == nil {
					return
				}
			}

			go enqueuePlay(m.Author, g, coll, sound)
			return
		}
	}
}

// Delete the message after a delay so the channel does not get cluttered
func deleteCommandMessage(s *discordgo.Session, channelID string, messageID string) {
	time.Sleep(30 * time.Second)
	err := s.ChannelMessageDelete(channelID, messageID)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err,
		}).Error("Failed to delete message.")
	}
}

// StartGidbig obviously
func StartGidbig() {
	Banner(nil)
	conf = cfg.LoadConfigFile()

	var err error

	// create SoundCollections by scanning the audio folder
	createCollections()

	// Start Webserver if a valid port is provided and if ClientID and ClientSecret are set
	if conf.Port != 0 && conf.Port >= 1 && conf.Ci != 0 && conf.Cs != "" && conf.RedirectURL != "" {
		log.Infoln("Starting web server on port " + strconv.Itoa(conf.Port))
		go startWebServer(conf)
	} else {
		log.Infoln("Required web server arguments missing or invalid. Skipping web server start.")
	}

	// Preload all the sounds
	log.Info("Preloading sounds...")
	for _, coll := range COLLECTIONS {
		coll.Load()
	}

	// Create a discord session
	log.Info("Starting discord session...")
	discord, err = discordgo.New("Bot " + conf.Token)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err,
		}).Fatal("Failed to create discord session")
		return
	}

	// Set sharding info
	discord.ShardID, _ = strconv.Atoi(conf.Shard)
	discord.ShardCount, _ = strconv.Atoi(conf.ShardCount)

	if discord.ShardCount <= 0 {
		discord.ShardCount = 1
	}

	discord.AddHandler(onReady)
	discord.AddHandler(onMessageCreate)

	err = discord.Open()
	if err != nil {
		log.WithFields(log.Fields{
			"error": err,
		}).Fatal("Failed to create discord websocket connection")
		return
	}

	//pkg callings
	go status.Start(discord)

	// We're running!
	log.Info("Gidbig is ready. Quit with CTRL-C.")

	banner := new(bytes.Buffer)
	Banner(banner)
	notifyOwner("```I just started!\n" + banner.String() + "```")

	// Wait for a signal to quit
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, os.Kill)
	<-c
}
