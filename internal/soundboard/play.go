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

	// Read the depth once so a runtime change cannot be applied half-way through
	// this enqueue (the check and the channel creation must agree).
	depth := m.queueMaxDepth()
	if exists {
		if !m.offer(guild.ID, p, depth) {
			slog.Info("soundboard: not queued, queue is full", "guild", guild.ID, "depth", depth)
		}
	} else {
		m.mu.Lock()
		m.queues[guild.ID] = make(chan *play, depth)
		m.mu.Unlock()
		_, _, err := m.playSound(p, nil, "")
		if err != nil {
			slog.Error("could not playSound", "error", err)
		}
	}
}

// offer appends p to an existing guild queue without ever blocking. It refuses
// when the queue already holds depth entries and also when the channel itself is
// full: a runtime increase of soundboard.queue_max_depth cannot grow a channel
// that already exists, so "len < depth" does not imply a free slot. A blocking
// send there would hold the queue mutex while the player goroutine needs that
// same mutex before it can drain the queue, which is a deadlock. The non-blocking
// send also covers the queue being deleted between the caller's lookup and this
// call, where a plain send on the now-absent (nil) channel would block forever.
func (m *Module) offer(guildID string, p *play, depth int) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	queue, ok := m.queues[guildID]
	if !ok || len(queue) >= depth {
		return false
	}
	select {
	case queue <- p:
		return true
	default:
		return false
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
