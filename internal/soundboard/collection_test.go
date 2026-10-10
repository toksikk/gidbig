package soundboard

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
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

func TestScontains(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		options []string
		want    bool
	}{
		{"found first", "foo", []string{"foo", "bar", "baz"}, true},
		{"found last", "baz", []string{"foo", "bar", "baz"}, true},
		{"not found", "qux", []string{"foo", "bar", "baz"}, false},
		{"empty options", "foo", []string{}, false},
		{"empty key", "", []string{"foo", ""}, true},
		{"exact match only", "fo", []string{"foo", "bar"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := scontains(tt.key, tt.options...)
			if got != tt.want {
				t.Errorf("scontains(%q, %v) = %v, want %v", tt.key, tt.options, got, tt.want)
			}
		})
	}
}

func TestCreateSound(t *testing.T) {
	s := createSound("test", 5, 250)
	if s == nil {
		t.Fatal("createSound() returned nil")
		return
	}
	if s.Name != "test" {
		t.Errorf("Name = %q, want %q", s.Name, "test")
	}
	if s.Weight != 5 {
		t.Errorf("Weight = %d, want 5", s.Weight)
	}
	if s.PartDelay != 250 {
		t.Errorf("PartDelay = %d, want 250", s.PartDelay)
	}
	if s.buffer == nil {
		t.Error("buffer should not be nil")
	}
	if len(s.buffer) != 0 {
		t.Errorf("buffer should be empty, got len %d", len(s.buffer))
	}
}

func TestAddNewSoundCollection(t *testing.T) {
	m := New()
	m.addNewSoundCollection("test", "sound1")

	if len(m.collections) != 1 {
		t.Fatalf("expected 1 collection, got %d", len(m.collections))
	}
	if m.collections[0].Prefix != "test" {
		t.Errorf("Prefix = %q, want %q", m.collections[0].Prefix, "test")
	}
	if len(m.collections[0].Commands) != 1 || m.collections[0].Commands[0] != "!test" {
		t.Errorf("Commands = %v, want [!test]", m.collections[0].Commands)
	}
	if len(m.collections[0].Sounds) != 1 || m.collections[0].Sounds[0].Name != "sound1" {
		t.Errorf("Sounds[0].Name = %q, want %q", m.collections[0].Sounds[0].Name, "sound1")
	}
}

func TestSoundCollectionRandom_ReturnsSound(t *testing.T) {
	sc := &soundCollection{
		Sounds: []*soundClip{
			createSound("alpha", 1, 250),
			createSound("beta", 2, 250),
			createSound("gamma", 3, 250),
		},
		soundRange: 6,
	}

	for i := 0; i < 100; i++ {
		got := sc.Random()
		if got == nil {
			t.Fatal("Random() returned nil")
		}
	}
}

func TestSoundCollectionRandom_RespectsWeights(t *testing.T) {
	heavy := createSound("heavy", 100, 250)
	light := createSound("light", 1, 250)
	sc := &soundCollection{
		Sounds:     []*soundClip{heavy, light},
		soundRange: 101,
	}

	heavyCount := 0
	for i := 0; i < 1000; i++ {
		got := sc.Random()
		if got.Name == "heavy" {
			heavyCount++
		}
	}

	// With weight 100:1, heavy should win at least 90% of the time
	if heavyCount < 900 {
		t.Errorf("heavy sound selected %d/1000 times, expected > 900 (weight 100:1)", heavyCount)
	}
}

func TestSoundCollectionRandom_SingleSound(t *testing.T) {
	sc := &soundCollection{
		Sounds:     []*soundClip{createSound("only", 1, 250)},
		soundRange: 1,
	}

	got := sc.Random()
	if got == nil {
		t.Fatal("Random() returned nil for single-sound collection")
		return
	}
	if got.Name != "only" {
		t.Errorf("Name = %q, want %q", got.Name, "only")
	}
}

func TestSoundCollectionRandom_emptyCollection(t *testing.T) {
	sc := &soundCollection{Prefix: "empty", soundRange: 0}
	if got := sc.Random(); got != nil {
		t.Errorf("Random() on empty collection = %v, want nil", got)
	}
}

func TestSoundCollection_Lookup_CaseInsensitive(t *testing.T) {
	sc := &soundCollection{Sounds: []*soundClip{{Name: "Airhorn"}}}
	if got := sc.Lookup("AIRHORN"); got == nil || got.Name != "Airhorn" {
		t.Errorf("Lookup(AIRHORN) = %#v", got)
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

// TestCreateCollections_scansAudioDir verifies the audio-directory scan builds
// one collection per prefix with every sound in it.
func TestCreateCollections_scansAudioDir(t *testing.T) {
	t.Chdir(t.TempDir())
	writeDCAFile(t, "alpha", "one", [][]byte{{0x01}})
	writeDCAFile(t, "alpha", "two", [][]byte{{0x02}})
	writeDCAFile(t, "beta", "three", [][]byte{{0x03}})

	m := New()
	m.createCollections()

	if len(m.collections) != 2 {
		t.Fatalf("collections = %d, want 2", len(m.collections))
	}
	alpha := m.collections[0]
	if alpha.Prefix != "alpha" {
		t.Fatalf("first prefix = %q, want alpha", alpha.Prefix)
	}
	if len(alpha.Sounds) != 2 {
		t.Fatalf("alpha sounds = %d, want 2", len(alpha.Sounds))
	}
	if !strings.EqualFold(m.collections[1].Prefix, "beta") {
		t.Errorf("second prefix = %q, want beta", m.collections[1].Prefix)
	}
}
