package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"io/ioutil"
	"math/rand"
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
	"github.com/toksikk/gidbig/pkg/wttrin"
	"gopkg.in/yaml.v2"
)

var (
	// discordgo session
	discord *discordgo.Session

	// Map of Guild id's to *Play channels, used for queuing and rate-limiting guilds
	queues = make(map[string]chan *Play)

	// bitrate Sound encoding settings
	bitrate = 128
	// maxQueueSize Sound encoding settings
	maxQueueSize = 6

	// OWNER variable
	OWNER string

	// mutex for checking if voice connection already exists
	mutex = &sync.Mutex{}
)

// Play represents an individual use of the !airhorn command
type Play struct {
	GuildID   string
	ChannelID string
	UserID    string
	Sound     *Sound

	// The next play to occur after this, only used for chaining sounds like anotha
	Next *Play

	// If true, this was a forced play using a specific airhorn sound name
	Forced bool
}

// SoundCollection of Sounds
type SoundCollection struct {
	Prefix     string
	Commands   []string
	Sounds     []*Sound
	ChainWith  *SoundCollection
	soundRange int
}

// Sound represents a sound clip
type Sound struct {
	Name string

	// Weight adjust how likely it is this song will play, higher = more likely
	Weight int

	// Delay (in milliseconds) for the bot to wait before sending the disconnect request
	PartDelay int

	// Buffer to store encoded PCM packets
	buffer [][]byte
}

// COLLECTIONS all collections
var COLLECTIONS []*SoundCollection

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
	var SC = &SoundCollection{
		Prefix: prefix,
		Commands: []string{
			"!" + prefix,
		},
		Sounds: []*Sound{
			createSound(soundname, 1, 250),
		},
	}
	COLLECTIONS = append(COLLECTIONS, SC)
}

// Create a Sound struct
func createSound(Name string, Weight int, PartDelay int) *Sound {
	return &Sound{
		Name:      Name,
		Weight:    Weight,
		PartDelay: PartDelay,
		buffer:    make([][]byte, 0),
	}
}

// Load soundcollection
func (sc *SoundCollection) Load() {
	for _, sound := range sc.Sounds {
		sc.soundRange += sound.Weight
		sound.Load(sc)
	}
}

// Random select sound
func (sc *SoundCollection) Random() *Sound {
	var (
		i      int
		number = randomRange(0, sc.soundRange)
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
func (s *Sound) Load(c *SoundCollection) error {
	path := fmt.Sprintf("audio/%v_%v.dca", c.Prefix, s.Name)

	file, err := os.Open(path)

	if err != nil {
		fmt.Println("error opening dca file :", err)
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
			fmt.Println("error reading from dca file :", err)
			return err
		}

		// read encoded pcm from dca file
		InBuf := make([]byte, opuslen)
		err = binary.Read(file, binary.LittleEndian, &InBuf)

		// Should not be any end of file errors
		if err != nil {
			fmt.Println("error reading from dca file :", err)
			return err
		}

		// append encoded pcm data to the buffer
		s.buffer = append(s.buffer, InBuf)
	}
}

// Play plays this sound over the specified VoiceConnection
func (s *Sound) Play(vc *discordgo.VoiceConnection) {
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

// Returns a random integer between min and max
func randomRange(min, max int) int {
	rand.Seed(time.Now().UTC().UnixNano())
	return rand.Intn(max-min) + min
}

// Prepares a play
func createPlay(user *discordgo.User, guild *discordgo.Guild, coll *SoundCollection, sound *Sound) *Play {
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
func enqueuePlay(user *discordgo.User, guild *discordgo.Guild, coll *SoundCollection, sound *Sound) {
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

func clearQueue(user *discordgo.User) {
	log.WithFields(log.Fields{
		"user": user,
	}).Info(user.Username + " triggered queue clearing")
	for key := range queues {
		delete(queues, key)
	}
	discord.Close()
	discord.Open()
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
	fmt.Fprintf(w, "Gidbig: \t%s\n", version)
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

// what did I start here?
func utilGetMentioned(s *discordgo.Session, m *discordgo.MessageCreate) *discordgo.User {
	for _, mention := range m.Mentions {
		if mention.ID != s.State.Ready.User.ID {
			return mention
		}
	}
	return nil
}

// Handles bot operator messages, should be refactored (lmao)
func handleBotControlMessages(s *discordgo.Session, m *discordgo.MessageCreate, parts []string, g *discordgo.Guild) {
	if len(parts) > 1 {
		if scontains(parts[1], "status") {
			displayBotStats(m.ChannelID)
		}
	}
}

func setIdleStatus() {
	games := []string{
		"Terranigma",
		"Secret of Mana",
		"Quake III Arena",
		"Duke Nukem 3D",
		"Monkey Island 2: LeChuck's Revenge",
		"Turtles in Time",
		"Unreal Tournament",
		"Half-Life",
		"Half-Life 2",
		"Warcraft II",
		"Starcraft",
		"Diablo",
		"Diablo II",
		"A Link to the Past",
		"Ocarina of Time",
		"Star Fox",
		"Tetris",
		"Pokémon Red",
		"Pokémon Blue",
		"Die Siedler II",
		"Day of the Tentacle",
		"Maniac Mansion",
		"Prince of Persia",
		"Super Mario Kart",
		"Pac-Man",
		"Frogger",
		"Donkey Kong",
		"Donkey Kong Country",
		"Asteroids",
		"Doom",
		"Breakout",
		"Street Fighter II",
		"Wolfenstein 3D",
		"Mega Man",
		"Myst",
		"R-Type",
	}
	for {
		discord.UpdateStreamingStatus(1, "", "")
		discord.UpdateGameStatus(0, games[randomRange(0, len(games))])
		time.Sleep(time.Duration(randomRange(5, 15)) * time.Minute)
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
	if len(m.Mentions) > 0 && m.Author.ID == OWNER && len(parts) > 0 {
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
	st, _ := discord.UserChannelCreate(OWNER)
	discord.ChannelMessageSend(st.ID, message)
}

func findSoundAndCollection(command string, soundname string) (*Sound, *SoundCollection) {
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
			wttr, err := wttrin.WeatherForToday(query[0] + "?format=4")
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
				wttr, err = wttrin.WeatherForToday(url.QueryEscape(query[0]) + ".png" + "?" + query[1])
			} else {
				wttr, err = wttrin.WeatherForToday(url.QueryEscape(query[0]) + ".png?0")
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
				wttr, err = wttrin.WeatherForTodayV2(url.QueryEscape(query[0]) + ".png" + "?" + query[1])
			} else {
				wttr, err = wttrin.WeatherForTodayV2(url.QueryEscape(query[0]) + ".png")
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
			var sound *Sound
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

type config struct {
	Token       string `yaml:"token"`
	Shard       string `yaml:"shard"`
	ShardCount  string `yaml:"shardcount"`
	Owner       string `yaml:"owner"`
	Port        int    `yaml:"port"`
	RedirectURL string `yaml:"redirecturl"`
	Ci          int    `yaml:"ci"`
	Cs          string `yaml:"cs"`
}

func loadConfigFile() *config {
	config := &config{}
	configFile, err := os.Open("config.yaml")
	if err != nil {
		log.Warningln("Could not load config file.", err)
		return nil
	}
	defer configFile.Close()

	d := yaml.NewDecoder(configFile)

	if err := d.Decode(&config); err != nil {
		return nil
	}

	return config
}

func main() {
	Banner()
	config := loadConfigFile()
	var (
		err error
	)

	// create SoundCollections by scanning the audio folder
	createCollections()

	// Start Webserver if a valid port is provided and if ClientID and ClientSecret are set
	if config.Port != 0 && config.Port >= 1 && config.Ci != 0 && config.Cs != "" && config.RedirectURL != "" {
		log.Infoln("Starting web server on port " + strconv.Itoa(config.Port))
		go startWebServer(strconv.Itoa(config.Port), strconv.Itoa(config.Ci), config.Cs, config.RedirectURL)
	} else {
		log.Infoln("Required web server arguments missing or invalid. Skipping web server start.")
	}

	if config.Owner != "" {
		OWNER = config.Owner
	}

	// Preload all the sounds
	log.Info("Preloading sounds...")
	for _, coll := range COLLECTIONS {
		coll.Load()
	}

	// Create a discord session
	log.Info("Starting discord session...")
	discord, err = discordgo.New("Bot " + config.Token)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err,
		}).Fatal("Failed to create discord session")
		return
	}

	// Set sharding info
	discord.ShardID, _ = strconv.Atoi(config.Shard)
	discord.ShardCount, _ = strconv.Atoi(config.ShardCount)

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

	go setIdleStatus()
	// We're running!
	log.Info("Gidbig is ready. Quit with CTRL-C.")

	go notifyOwner("I just started.")

	// Wait for a signal to quit
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, os.Kill)
	<-c
}
