package gidbig

import (
	"sync"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
)

// TestDAVEHandshakeWarmup_minimum verifies that the DAVE warmup constant is at
// least 400 ms.  Discord's DAVE (E2EE) key exchange requires roughly 100–300 ms
// (two gateway round-trips: key-package → Welcome, then ReadyForTransition →
// ExecuteTransition).  Frames sent before ExecuteTransition is processed are
// discarded by Discord clients in DAVE-enabled channels.  A warmup below 400 ms
// risks the exchange not completing before audio starts, especially on connections
// with higher latency.
func TestDAVEHandshakeWarmup_minimum(t *testing.T) {
	const minWarmup = 400 * time.Millisecond
	if daveHandshakeWarmup < minWarmup {
		t.Errorf("daveHandshakeWarmup = %v; must be >= %v to cover the DAVE key exchange on slow connections", daveHandshakeWarmup, minWarmup)
	}
}

// newTestVoiceConnection creates a minimal VoiceConnection suitable for unit-testing
// the Play method.  Speaking() calls will fail (no WebSocket), but the OpusSend
// channel is fully functional so frame delivery and timing can be verified.
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

// TestSoundClipPlay_pacingMatchesFrameDuration verifies that Play takes
// approximately N*opusFrameDuration to send N frames, ensuring that
// Speaking(false) is not called before the audio is sent.
func TestSoundClipPlay_pacingMatchesFrameDuration(t *testing.T) {
	const (
		n              = 3
		toleranceLow   = 2 // divisor: actual must be >= want/toleranceLow
		toleranceHigh  = 2 // multiplier: actual must be <= want*toleranceHigh
	)
	frames := make([][]byte, n)
	for i := range frames {
		frames[i] = []byte{byte(i)}
	}

	s := &soundClip{buffer: frames}
	vc := newTestVoiceConnection(n + 4)

	start := time.Now()
	s.Play(vc)
	elapsed := time.Since(start)

	want := time.Duration(n) * opusFrameDuration
	// Allow +/-50 % tolerance to keep the test robust under scheduling jitter.
	low := want / toleranceLow
	high := want * toleranceHigh
	if elapsed < low || elapsed > high {
		t.Errorf("Play took %v; want in [%v, %v] for %d frames", elapsed, low, high, n)
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
	// Should complete in well under one frame duration.
	if elapsed > opusFrameDuration {
		t.Errorf("empty Play took %v; expected < %v", elapsed, opusFrameDuration)
	}
}
