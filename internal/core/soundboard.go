package gidbig

import (
	"log/slog"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/util"
)

var (
	// Map of Guild id's to *Play channels, used for queuing and rate-limiting guilds
	queues = make(map[string]chan *Play)

	// bitrate Sound encoding settings
	// bitrate = 128

	// maxQueueSize Sound encoding settings
	maxQueueSize = 6
)

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

// Play plays this sound over the specified VoiceConnection
func (s *soundClip) Play(vc *discordgo.VoiceConnection) {
	err := vc.Speaking(true)
	if err != nil {
		slog.Error("error setting setting speaking to true")
	}
	defer func() {
		err := vc.Speaking(false)
		if err != nil {
			slog.Error("error setting setting speaking to false")
		}
	}()

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
		slog.Info("Playing sound", "username", user.Username, "prefix", coll.Prefix, "soundname", sound.Name, "server", guild.Name, "channel", play.ChannelID)
	} else {
		slog.Info("Playing random sound", "username", user.Username, "prefix", coll.Prefix, "soundname", sound.Name, "server", guild.Name, "channel", play.ChannelID)
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
		err := playSound(play, nil)
		if err != nil {
			slog.Error("could not playSound", "error", err)
		}
	}
}

// Play a sound
func playSound(play *Play, vc *discordgo.VoiceConnection) (err error) {
	slog.Info("Playing sound", "play", play)

	if vc != nil {
		if vc.GuildID != play.GuildID {
			err := vc.Disconnect()
			if err != nil {
				slog.Error("could not disconnect voice connection", "error", err)
			}
			vc = nil
		}
	}

	if vc == nil {
		vc, err = discord.ChannelVoiceJoin(play.GuildID, play.ChannelID, false, true)
		if err != nil {
			slog.Error("Failed to play sound", "error", err)
			mutex.Lock()
			delete(queues, play.GuildID)
			mutex.Unlock()
			return err
		}
	}

	// If we need to change channels, do that now
	if vc.ChannelID != play.ChannelID {
		err := vc.ChangeChannel(play.ChannelID, false, true)
		if err != nil {
			slog.Error("could not change voice channel", "error", err)
		}
		time.Sleep(time.Millisecond * 125)
	}

	// Sleep for a specified amount of time before playing the sound
	time.Sleep(time.Millisecond * 32)

	// Play the sound
	play.Sound.Play(vc)

	// If this is chained, play the chained sound
	if play.Next != nil {
		err := playSound(play.Next, vc)
		if err != nil {
			slog.Error("could not playSound", "error", err)
		}
	}

	// If there is another song in the queue, recurse and play that
	if len(queues[play.GuildID]) > 0 {
		play = <-queues[play.GuildID]
		err := playSound(play, vc)
		if err != nil {
			slog.Error("could not playSound", "error", err)
		}
		return nil
	}

	// If the queue is empty, delete it
	time.Sleep(time.Millisecond * time.Duration(play.Sound.PartDelay))
	mutex.Lock()
	delete(queues, play.GuildID)
	err = vc.Disconnect()
	if err != nil {
		slog.Error("could not disconnect voice connection", "error", err)
		return err
	}
	mutex.Unlock()
	return nil
}
