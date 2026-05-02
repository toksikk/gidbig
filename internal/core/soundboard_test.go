package gidbig

import (
	"sync"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
)

// newTestVoiceConnection creates a minimal VoiceConnection suitable for unit-testing
// the Play method.  Speaking() calls will fail (no WebSocket), but the OpusSend
// channel is fully functional so frame delivery can be verified.
func newTestVoiceConnection(bufSize int) *discordgo.VoiceConnection {
	vc := &discordgo.VoiceConnection{}
	vc.Cond = sync.NewCond(&sync.Mutex{})
	vc.OpusSend = make(chan []byte, bufSize)
	return vc
}

// TestSoundClipPlay_deliversAllFrames verifies that every Opus frame in the
// buffer is forwarded to OpusSend.
func TestSoundClipPlay_deliversAllFrames(t *testing.T) {
	frames := [][]byte{{0x01}, {0x02}, {0x03}}
	s := &soundClip{buffer: frames}
	vc := newTestVoiceConnection(len(frames) + 4)

	s.Play(vc)

	if got := len(vc.OpusSend); got != len(frames) {
		t.Fatalf("OpusSend has %d frames, want %d", got, len(frames))
	}
	for i := 0; i < len(frames); i++ {
		got := <-vc.OpusSend
		if len(got) != 1 || got[0] != frames[i][0] {
			t.Errorf("frame %d = %v, want %v", i, got, frames[i])
		}
	}
}

// TestSoundClipPlay_doesNotSelfPace verifies that Play does not duplicate the
// 20 ms transmit cadence that discordgo's opusSender already provides.  With a
// channel buffer large enough to hold every frame, Play must finish well below
// the time it would take if each iteration slept one frame's worth.
func TestSoundClipPlay_doesNotSelfPace(t *testing.T) {
	const n = 10
	frames := make([][]byte, n)
	for i := range frames {
		frames[i] = []byte{byte(i)}
	}

	s := &soundClip{buffer: frames}
	vc := newTestVoiceConnection(n + 4)

	start := time.Now()
	s.Play(vc)
	elapsed := time.Since(start)

	// One frame interval is 20 ms.  If Play self-paces, n frames take >= n*20 ms.
	// Pushing into a non-blocking channel should be orders of magnitude faster.
	const maxAcceptable = 20 * time.Millisecond
	if elapsed >= maxAcceptable {
		t.Errorf("Play(%d frames) took %v; expected < %v (no self-pacing)", n, elapsed, maxAcceptable)
	}
}

// TestSoundClipPlay_emptyBuffer verifies that Play on an empty clip completes
// instantly without sending any frames.
func TestSoundClipPlay_emptyBuffer(t *testing.T) {
	s := &soundClip{buffer: nil}
	vc := newTestVoiceConnection(4)

	start := time.Now()
	s.Play(vc)
	elapsed := time.Since(start)

	if len(vc.OpusSend) != 0 {
		t.Errorf("expected 0 frames in OpusSend, got %d", len(vc.OpusSend))
	}
	if elapsed > 5*time.Millisecond {
		t.Errorf("empty Play took %v; expected near-instant", elapsed)
	}
}
