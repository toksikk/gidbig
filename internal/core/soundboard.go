package gidbig

import (
	"context"
	"log/slog"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/util"
)

// playStartDelay matches the pre-roll used by the upstream airhorn example.
// ChannelVoiceJoin returns when Status==Ready (AEAD cipher up, opusSender
// goroutine running), but on DAVE-enabled channels the Welcome handshake
// finishes a few hundred ms after Ready.  A short pause here gives that
// handshake time to complete before the first frame is queued.
const playStartDelay = 250 * time.Millisecond

var (
	// Map of Guild id's to *Play channels, used for queuing and rate-limiting guilds
	queues = make(map[string]chan *Play)

	// nowPlaying tracks the sound currently being played per guild
	nowPlaying = make(map[string]*Play)

	// maxQueueSize Sound encoding settings
	maxQueueSize = 6
)

// Random select sound
func (sc *soundCollection) Random() *soundClip {
	if len(sc.Sounds) == 0 {
		return nil
	}
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

// Play plays this sound over the specified VoiceConnection.
//
// discordgo's opusSender drives the 20 ms transmit cadence with its own
// time.Ticker — this loop only pushes Opus frames into the buffered OpusSend
// channel and lets backpressure pace the writes.  Adding a per-frame sleep
// here (as PR #110 did) duplicates the cadence and starves the sender's
// channel between ticks, which is itself a plausible cause of the silent
// audio reported in #113.
func (s *soundClip) Play(vc *discordgo.VoiceConnection) {
	slog.Debug("Play start", "frames", len(s.buffer), "guildID", vc.GuildID, "status", vc.Status)

	if err := vc.Speaking(true); err != nil {
		slog.Error("error setting speaking to true", "error", err)
	}
	defer func() {
		if err := vc.Speaking(false); err != nil {
			slog.Error("error setting speaking to false", "error", err)
		}
	}()

	for i, buff := range s.buffer {
		select {
		case vc.OpusSend <- buff:
		case <-time.After(time.Second):
			slog.Error("OpusSend stalled — sender goroutine is not draining frames",
				"guildID", vc.GuildID, "frameIndex", i, "totalFrames", len(s.buffer), "status", vc.Status)
			return
		}
	}

	slog.Debug("Play done", "frames", len(s.buffer), "guildID", vc.GuildID)
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
		slog.Warn("Failed to find channel to play sound in", "user", user.ID, "guild", guild.ID)
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
	if play.Sound == nil {
		slog.Warn("sound collection is empty, nothing to play", "prefix", coll.Prefix)
		return nil
	}

	// If the collection is a chained one, set the next sound
	if coll.ChainWith != nil {
		nextSound := coll.ChainWith.Random()
		if nextSound == nil {
			slog.Warn("chained collection is empty, skipping next sound", "prefix", coll.ChainWith.Prefix)
		} else {
			play.Next = &Play{
				GuildID:   play.GuildID,
				ChannelID: play.ChannelID,
				UserID:    play.UserID,
				Sound:     nextSound,
				Forced:    play.Forced,
			}
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
		slog.Info("Playing sound", "username", user.Username, "prefix", coll.Prefix, "soundname", sound.Name, "server", guild.Name, "channel", play.ChannelID)
	} else {
		slog.Info("Playing random sound", "username", user.Username, "prefix", coll.Prefix, "soundname", play.Sound.Name, "server", guild.Name, "channel", play.ChannelID)
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
		_, _, err := playSound(play, nil, "")
		if err != nil {
			slog.Error("could not playSound", "error", err)
		}
	}
}

// Play a sound
func playSound(play *Play, vc *discordgo.VoiceConnection, vcChannelID string) (retVC *discordgo.VoiceConnection, retChannelID string, err error) {
	slog.Info("Playing sound", "play", play)

	ctx := context.Background()

	if vc != nil {
		if vc.GuildID != play.GuildID {
			if disconnErr := vc.Disconnect(ctx); disconnErr != nil {
				slog.Error("could not disconnect voice connection", "error", disconnErr)
			}
			vc = nil
			vcChannelID = ""
		}
	}

	if vc == nil {
		vc, err = discord.ChannelVoiceJoin(ctx, play.GuildID, play.ChannelID, false, true)
		if err != nil {
			slog.Error("Failed to play sound", "error", err)
			mutex.Lock()
			delete(queues, play.GuildID)
			mutex.Unlock()
			return nil, "", err
		}
		vcChannelID = play.ChannelID
		time.Sleep(playStartDelay)
	}

	// If we need to change channels, disconnect and rejoin
	if vcChannelID != play.ChannelID {
		if disconnErr := vc.Disconnect(ctx); disconnErr != nil {
			slog.Error("could not disconnect voice connection", "error", disconnErr)
		}
		vc, err = discord.ChannelVoiceJoin(ctx, play.GuildID, play.ChannelID, false, true)
		if err != nil {
			slog.Error("could not join voice channel", "error", err)
			mutex.Lock()
			delete(queues, play.GuildID)
			mutex.Unlock()
			return nil, "", err
		}
		vcChannelID = play.ChannelID
		time.Sleep(playStartDelay)
	}

	mutex.Lock()
	nowPlaying[play.GuildID] = play
	mutex.Unlock()

	// Play the sound
	play.Sound.Play(vc)

	mutex.Lock()
	delete(nowPlaying, play.GuildID)
	mutex.Unlock()

	// If this is chained, play the chained sound
	if play.Next != nil {
		vc, vcChannelID, err = playSound(play.Next, vc, vcChannelID)
		if err != nil {
			slog.Error("could not playSound", "error", err)
		}
	}

	// If there is another song in the queue, recurse and play that
	if len(queues[play.GuildID]) > 0 {
		play = <-queues[play.GuildID]
		vc, vcChannelID, err = playSound(play, vc, vcChannelID)
		if err != nil {
			slog.Error("could not playSound", "error", err)
		}
		return vc, vcChannelID, nil
	}

	// If the queue is empty, delete it
	time.Sleep(time.Millisecond * time.Duration(play.Sound.PartDelay))
	mutex.Lock()
	delete(queues, play.GuildID)
	if disconnErr := vc.Disconnect(context.Background()); disconnErr != nil {
		slog.Error("could not disconnect voice connection", "error", disconnErr)
	}
	mutex.Unlock()
	return nil, "", nil
}
