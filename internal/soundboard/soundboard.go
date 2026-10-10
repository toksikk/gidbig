package soundboard

import (
	"context"
	"log/slog"
	"strconv"
	"sync"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/bot"
)

// defaultMaxQueueSize is the per-guild queue depth used when the config does
// not override it.
const defaultMaxQueueSize = 6

// Module implements bot.Module for the soundboard plugin.
type Module struct {
	session *discordgo.Session

	mu           sync.Mutex
	collections  []*soundCollection
	queues       map[string]chan *Play
	nowPlaying   map[string]*Play
	maxQueueSize int

	// enqueue queues a play asynchronously; overridable in tests.
	enqueue func(*discordgo.User, *discordgo.Guild, *soundCollection, *soundClip)
}

// New returns a Module with production-default hooks.
func New() *Module {
	m := &Module{
		queues:       make(map[string]chan *Play),
		nowPlaying:   make(map[string]*Play),
		maxQueueSize: defaultMaxQueueSize,
	}
	m.enqueue = func(user *discordgo.User, guild *discordgo.Guild, coll *soundCollection, sound *soundClip) {
		go m.enqueuePlay(user, guild, coll, sound)
	}
	return m
}

// Name returns the module's identifier.
func (m *Module) Name() string { return "soundboard" }

// Init loads the audio collections and prepares the per-guild play queues.
func (m *Module) Init(d bot.Deps) error {
	m.session = d.Session
	if d.Config != nil && d.Config.Soundboard.QueueMaxDepth > 0 {
		m.maxQueueSize = d.Config.Soundboard.QueueMaxDepth
	}

	slog.Info("Preloading sounds...")
	m.createCollections()
	for _, coll := range m.collections {
		coll.Load()
	}
	slog.Info("soundboard: initialized", "collections", len(m.collections))
	return nil
}

// Commands returns the slash command definitions for this module.
func (m *Module) Commands() []*discordgo.ApplicationCommand {
	return []*discordgo.ApplicationCommand{
		{
			Name:        "list",
			Description: "List all available sound effects",
		},
		{
			Name:        "play",
			Description: "Play a sound effect (random if sound is omitted)",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "collection",
					Description: "Sound collection to play from",
					Required:    true,
					MaxLength:   100,
				},
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "sound",
					Description: "Specific sound effect name (optional)",
					Required:    false,
					MaxLength:   100,
				},
			},
		},
	}
}

// Listeners returns the Discord event listeners for this module.
func (m *Module) Listeners() []bot.EventListener {
	return []bot.EventListener{m.onInteractionCreate}
}

// Components returns no message-component handlers for this module.
func (m *Module) Components() []bot.ComponentHandler { return nil }

// Background returns no supervised background tasks for this module.
func (m *Module) Background() []bot.BackgroundTask { return nil }

// Shutdown releases module resources. Voice connections are closed as each
// playback finishes, so there is nothing to tear down here.
func (m *Module) Shutdown() error { return nil }

// Collections returns a snapshot of the loaded sound collections.
func (m *Module) Collections() []Collection {
	out := make([]Collection, 0, len(m.collections))
	for _, c := range m.collections {
		sounds := make([]Sound, 0, len(c.Sounds))
		for _, s := range c.Sounds {
			sounds = append(sounds, Sound{Name: s.Name})
		}
		out = append(out, Collection{Prefix: c.Prefix, Sounds: sounds})
	}
	return out
}

// Enqueue plays a sound from the named collection in the guild where the user
// is currently in a voice channel. An empty or unknown sound name falls back
// to a random sound from the collection. It returns false when the collection
// is unknown or the user/guild is invalid.
func (m *Module) Enqueue(user *discordgo.User, guild *discordgo.Guild, command, soundname string) bool {
	if user == nil || guild == nil {
		return false
	}
	sound, coll := m.findSoundAndCollection(command, soundname)
	if coll == nil {
		return false
	}
	if sound == nil {
		sound = coll.Random()
	}
	m.enqueue(user, guild, coll, sound)
	return true
}

// QueueStatus describes the queue state for a single guild.
type QueueStatus struct {
	GuildID     string
	NowPlaying  string
	QueueLength int
}

// QueueStatus returns a snapshot of per-guild queue lengths and the sound
// currently playing in each guild.
func (m *Module) QueueStatus() []QueueStatus {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]QueueStatus, 0, len(m.queues))
	for guildID, ch := range m.queues {
		qs := QueueStatus{GuildID: guildID, QueueLength: len(ch)}
		if np, ok := m.nowPlaying[guildID]; ok && np != nil && np.Sound != nil {
			qs.NowPlaying = np.Sound.Name
		}
		out = append(out, qs)
	}
	return out
}

// Stats reports soundboard runtime counters for /status.
func (m *Module) Stats(context.Context) (bot.ModuleStats, error) {
	sounds := 0
	for _, c := range m.collections {
		sounds += len(c.Sounds)
	}
	m.mu.Lock()
	queued, active := 0, len(m.queues)
	for _, q := range m.queues {
		queued += len(q)
	}
	playing := len(m.nowPlaying)
	m.mu.Unlock()

	return bot.ModuleStats{
		Summary: []bot.Stat{
			{Name: "sounds", Value: strconv.Itoa(sounds)},
			{Name: "collections", Value: strconv.Itoa(len(m.collections))},
			{Name: "queued", Value: strconv.Itoa(queued)},
		},
		Detail: []bot.Stat{
			{Name: "playing", Value: strconv.Itoa(playing)},
			{Name: "active queues", Value: strconv.Itoa(active)},
			{Name: "max queue", Value: strconv.Itoa(m.maxQueueSize)},
		},
	}, nil
}
