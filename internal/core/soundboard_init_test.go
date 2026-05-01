package gidbig

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func writeDCAFile(t *testing.T, prefix, name string, frames [][]byte) {
	t.Helper()
	if err := os.MkdirAll("audio", 0o755); err != nil {
		t.Fatalf("mkdir audio: %v", err)
	}
	path := filepath.Join("audio", prefix+"_"+name+".dca")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()
	for _, frame := range frames {
		if err := binary.Write(f, binary.LittleEndian, int16(len(frame))); err != nil {
			t.Fatalf("write frame length: %v", err)
		}
		if _, err := f.Write(frame); err != nil {
			t.Fatalf("write frame data: %v", err)
		}
	}
}

func TestSoundClipLoad_missingFile(t *testing.T) {
	t.Chdir(t.TempDir())

	c := &soundCollection{Prefix: "missing"}
	s := &soundClip{Name: "clip", buffer: make([][]byte, 0)}
	if err := s.Load(c); err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestSoundClipLoad_emptyFile(t *testing.T) {
	t.Chdir(t.TempDir())
	writeDCAFile(t, "empty", "clip", nil)

	c := &soundCollection{Prefix: "empty"}
	s := &soundClip{Name: "clip", buffer: make([][]byte, 0)}
	if err := s.Load(c); err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if len(s.buffer) != 0 {
		t.Errorf("buffer len = %d, want 0", len(s.buffer))
	}
}

func TestSoundClipLoad_readsFrames(t *testing.T) {
	t.Chdir(t.TempDir())
	frames := [][]byte{
		{0x01, 0x02, 0x03, 0x04},
		{0xff, 0xee, 0xdd},
	}
	writeDCAFile(t, "frames", "clip", frames)

	c := &soundCollection{Prefix: "frames"}
	s := &soundClip{Name: "clip", buffer: make([][]byte, 0)}
	if err := s.Load(c); err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if len(s.buffer) != len(frames) {
		t.Fatalf("buffer len = %d, want %d", len(s.buffer), len(frames))
	}
	for i, want := range frames {
		got := s.buffer[i]
		if len(got) != len(want) {
			t.Errorf("frame %d len = %d, want %d", i, len(got), len(want))
			continue
		}
		for j := range want {
			if got[j] != want[j] {
				t.Errorf("frame %d byte %d = %#x, want %#x", i, j, got[j], want[j])
			}
		}
	}
}

func TestSoundClipLoad_doesNotLeakFD(t *testing.T) {
	t.Chdir(t.TempDir())
	writeDCAFile(t, "leak", "clip", [][]byte{{0xaa, 0xbb}})

	c := &soundCollection{Prefix: "leak"}
	for i := 0; i < 5000; i++ {
		s := &soundClip{Name: "clip", buffer: make([][]byte, 0)}
		if err := s.Load(c); err != nil {
			t.Fatalf("call %d returned error: %v", i, err)
		}
	}
}

func TestSoundCollectionRandom_emptyCollection(t *testing.T) {
	sc := &soundCollection{Prefix: "empty", soundRange: 0}
	if got := sc.Random(); got != nil {
		t.Errorf("Random() on empty collection = %v, want nil", got)
	}
}

func TestSoundCollectionRandom_returnsSound(t *testing.T) {
	clip := &soundClip{Name: "beep", Weight: 1}
	sc := &soundCollection{
		Prefix:     "test",
		Sounds:     []*soundClip{clip},
		soundRange: 1,
	}
	for i := 0; i < 20; i++ {
		got := sc.Random()
		if got == nil {
			t.Fatal("Random() returned nil for non-empty collection")
		}
		if got.Name != "beep" {
			t.Errorf("Random() = %q, want %q", got.Name, "beep")
		}
	}
}
