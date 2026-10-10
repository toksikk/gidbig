package soundboard

import (
	"context"
	"log/slog"
	"time"

	"github.com/bwmarrin/discordgo"
)

// playStartDelay matches the pre-roll used by the upstream airhorn example.
// ChannelVoiceJoin returns when Status==Ready (AEAD cipher up, opusSender
// goroutine running), but on DAVE-enabled channels the Welcome handshake
// finishes a few hundred ms after Ready.  A short pause here gives that
// handshake time to complete before the first frame is queued.
const playStartDelay = 250 * time.Millisecond

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

// getCurrentVoiceChannel attempts to find the current user's voice channel
// inside a given guild.
func (m *Module) getCurrentVoiceChannel(user *discordgo.User, guild *discordgo.Guild) *discordgo.Channel {
	for _, vs := range guild.VoiceStates {
		if vs.UserID == user.ID {
			channel, _ := m.session.State.Channel(vs.ChannelID)
			return channel
		}
	}
	return nil
}

// createPlay prepares a play.
func (m *Module) createPlay(user *discordgo.User, guild *discordgo.Guild, coll *soundCollection, sound *soundClip) *play {
	// Grab the users voice channel
	channel := m.getCurrentVoiceChannel(user, guild)
	if channel == nil {
		slog.Warn("Failed to find channel to play sound in", "user", user.ID, "guild", guild.ID)
		return nil
	}

	// Create the play
	p := &play{
		GuildID:   guild.ID,
		ChannelID: channel.ID,
		UserID:    user.ID,
		Sound:     sound,
		Forced:    true,
	}

	// If we didn't get passed a manual sound, generate a random one
	if p.Sound == nil {
		p.Sound = coll.Random()
		p.Forced = false
	}
	if p.Sound == nil {
		slog.Warn("sound collection is empty, nothing to play", "prefix", coll.Prefix)
		return nil
	}

	// If the collection is a chained one, set the next sound
	if coll.ChainWith != nil {
		nextSound := coll.ChainWith.Random()
		if nextSound == nil {
			slog.Warn("chained collection is empty, skipping next sound", "prefix", coll.ChainWith.Prefix)
		} else {
			p.Next = &play{
				GuildID:   p.GuildID,
				ChannelID: p.ChannelID,
				UserID:    p.UserID,
				Sound:     nextSound,
				Forced:    p.Forced,
			}
		}
	}

	return p
}

// enqueuePlay prepares and enqueues a play into the ratelimit/buffer guild queue.
func (m *Module) enqueuePlay(user *discordgo.User, guild *discordgo.Guild, coll *soundCollection, sound *soundClip) {
	p := m.createPlay(user, guild, coll, sound)
	if p == nil {
		return
	}
	if sound != nil {
		slog.Info("Playing sound", "username", user.Username, "prefix", coll.Prefix, "soundname", sound.Name, "server", guild.Name, "channel", p.ChannelID)
	} else {
		slog.Info("Playing random sound", "username", user.Username, "prefix", coll.Prefix, "soundname", p.Sound.Name, "server", guild.Name, "channel", p.ChannelID)
	}
	// Check if we already have a connection to this guild
	// this should be threadsafe
	m.mu.Lock()
	_, exists := m.queues[guild.ID]
	m.mu.Unlock()

	if exists {
		if len(m.queues[guild.ID]) < m.maxQueueSize {
			m.mu.Lock()
			m.queues[guild.ID] <- p
			m.mu.Unlock()
		}
	} else {
		m.mu.Lock()
		m.queues[guild.ID] = make(chan *play, m.maxQueueSize)
		m.mu.Unlock()
		_, _, err := m.playSound(p, nil, "")
		if err != nil {
			slog.Error("could not playSound", "error", err)
		}
	}
}

// playSound plays a sound, joining or moving voice connections as needed.
func (m *Module) playSound(p *play, vc *discordgo.VoiceConnection, vcChannelID string) (retVC *discordgo.VoiceConnection, retChannelID string, err error) {
	slog.Info("Playing sound", "play", p)

	ctx := context.Background()

	if vc != nil {
		if vc.GuildID != p.GuildID {
			if disconnErr := vc.Disconnect(ctx); disconnErr != nil {
				slog.Error("could not disconnect voice connection", "error", disconnErr)
			}
			vc = nil
			vcChannelID = ""
		}
	}

	if vc == nil {
		vc, err = m.session.ChannelVoiceJoin(ctx, p.GuildID, p.ChannelID, false, true)
		if err != nil {
			slog.Error("Failed to play sound", "error", err)
			m.mu.Lock()
			delete(m.queues, p.GuildID)
			m.mu.Unlock()
			return nil, "", err
		}
		vcChannelID = p.ChannelID
		time.Sleep(playStartDelay)
	}

	// If we need to change channels, disconnect and rejoin
	if vcChannelID != p.ChannelID {
		if disconnErr := vc.Disconnect(ctx); disconnErr != nil {
			slog.Error("could not disconnect voice connection", "error", disconnErr)
		}
		vc, err = m.session.ChannelVoiceJoin(ctx, p.GuildID, p.ChannelID, false, true)
		if err != nil {
			slog.Error("could not join voice channel", "error", err)
			m.mu.Lock()
			delete(m.queues, p.GuildID)
			m.mu.Unlock()
			return nil, "", err
		}
		vcChannelID = p.ChannelID
		time.Sleep(playStartDelay)
	}

	m.mu.Lock()
	m.nowPlaying[p.GuildID] = p
	m.mu.Unlock()

	// Play the sound
	p.Sound.Play(vc)

	m.mu.Lock()
	delete(m.nowPlaying, p.GuildID)
	m.mu.Unlock()

	// If this is chained, play the chained sound
	if p.Next != nil {
		vc, vcChannelID, err = m.playSound(p.Next, vc, vcChannelID)
		if err != nil {
			slog.Error("could not playSound", "error", err)
		}
	}

	// If there is another song in the queue, recurse and play that
	if len(m.queues[p.GuildID]) > 0 {
		p = <-m.queues[p.GuildID]
		vc, vcChannelID, err = m.playSound(p, vc, vcChannelID)
		if err != nil {
			slog.Error("could not playSound", "error", err)
		}
		return vc, vcChannelID, nil
	}

	// If the queue is empty, delete it
	time.Sleep(time.Millisecond * time.Duration(p.Sound.PartDelay))
	m.mu.Lock()
	delete(m.queues, p.GuildID)
	if disconnErr := vc.Disconnect(context.Background()); disconnErr != nil {
		slog.Error("could not disconnect voice connection", "error", disconnErr)
	}
	m.mu.Unlock()
	return nil, "", nil
}
