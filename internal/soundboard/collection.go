package soundboard

import (
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/toksikk/gidbig/internal/util"
)

// Collection describes a loaded sound collection for external consumers such as
// the web UI.
type Collection struct {
	Prefix string
	Sounds []Sound
}

// Sound describes a single sound clip within a collection.
type Sound struct {
	Name string
}

// Play represents an individual queued sound playback.
type Play struct {
	GuildID   string
	ChannelID string
	UserID    string
	Sound     *soundClip

	// The next play to occur after this, only used for chaining sounds like anotha
	Next *Play

	// If true, this was a forced play using a specific sound name
	Forced bool
}

// soundCollection of sound clips
type soundCollection struct {
	Prefix     string
	Commands   []string
	Sounds     []*soundClip
	ChainWith  *soundCollection
	soundRange int
}

// Lookup returns the first sound with the given name (case-insensitive), or nil.
func (sc *soundCollection) Lookup(name string) *soundClip {
	lower := strings.ToLower(name)
	for _, s := range sc.Sounds {
		if strings.ToLower(s.Name) == lower {
			return s
		}
	}
	return nil
}

// soundClip represents a sound clip
type soundClip struct {
	Name string

	// Weight adjust how likely it is this song will play, higher = more likely
	Weight int

	// Delay (in milliseconds) for the bot to wait before sending the disconnect request
	PartDelay int

	// Buffer to store encoded PCM packets
	buffer [][]byte
}

// scontains reports whether key equals any of the given options.
func scontains(key string, options ...string) bool {
	for _, item := range options {
		if item == key {
			return true
		}
	}
	return false
}

// createCollections scans ./audio for .dca files and builds the sound collections.
func (m *Module) createCollections() {
	files, _ := os.ReadDir("./audio")
	for _, f := range files {
		if strings.Contains(f.Name(), ".dca") {
			soundfile := strings.Split(strings.ReplaceAll(f.Name(), ".dca", ""), "_")
			containsPrefix := false
			containsSound := false

			if len(m.collections) == 0 {
				m.addNewSoundCollection(soundfile[0], soundfile[1])
			}
			for _, c := range m.collections {
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
				m.addNewSoundCollection(soundfile[0], soundfile[1])
			}
		}
	}
}

func (m *Module) addNewSoundCollection(prefix string, soundname string) {
	var sc = &soundCollection{
		Prefix: prefix,
		Commands: []string{
			"!" + prefix,
		},
		Sounds: []*soundClip{
			createSound(soundname, 1, 250),
		},
	}
	m.collections = append(m.collections, sc)
}

// createSound builds a sound clip with an empty frame buffer.
func createSound(Name string, Weight int, PartDelay int) *soundClip {
	return &soundClip{
		Name:      Name,
		Weight:    Weight,
		PartDelay: PartDelay,
		buffer:    make([][]byte, 0),
	}
}

// findSoundAndCollection resolves a web-UI command/sound pair to a sound clip
// and its collection. When the command matches a collection but the sound name
// does not, it returns a nil clip and the collection so the caller can fall
// back to a random sound.
func (m *Module) findSoundAndCollection(command string, soundname string) (*soundClip, *soundCollection) {
	for _, c := range m.collections {
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

// Random selects a weighted-random sound from the collection.
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

// Load loads every sound in the collection from disk and accumulates weights.
func (sc *soundCollection) Load() {
	for _, sound := range sc.Sounds {
		sc.soundRange += sound.Weight
		err := sound.Load(sc)
		if err != nil {
			slog.Error("error adding sound to soundCollection", "Error", err)
		}
	}
}

// Load attempts to load an encoded sound file from disk.
// DCA files are pre-computed sound files that are easy to send to Discord.
// If you would like to create your own DCA files, please use:
// https://github.com/nstafie/dca-rs
// eg: dca-rs --raw -i <input wav file> > <output file>
func (s *soundClip) Load(c *soundCollection) error {
	path := fmt.Sprintf("audio/%v_%v.dca", c.Prefix, s.Name)

	file, err := os.Open(path)

	if err != nil {
		slog.Error("error opening dca file", "error", err)
		return err
	}
	defer func() { _ = file.Close() }()

	var opuslen int16
	var minLen, maxLen, totalBytes int

	for {
		// read opus frame length from dca file
		err = binary.Read(file, binary.LittleEndian, &opuslen)

		// If this is the end of the file, just return
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		}

		if err != nil {
			slog.Error("error reading from dca file", "error", err)
			return err
		}

		// A negative or absurdly large frame length means we lost framing —
		// most often because the file has a DCA1 JSON header that the loader
		// doesn't strip.  Surface this loudly instead of corrupting the buffer.
		if opuslen <= 0 || opuslen > 4000 {
			slog.Error("dca frame length out of range — file is likely DCA1 or corrupted",
				"path", path, "opuslen", opuslen, "frameIndex", len(s.buffer))
			return fmt.Errorf("invalid opus frame length %d in %s", opuslen, path)
		}

		// read encoded pcm from dca file
		InBuf := make([]byte, opuslen)
		err = binary.Read(file, binary.LittleEndian, &InBuf)

		// Should not be any end of file errors
		if err != nil {
			slog.Error("error reading from dca file", "error", err)
			return err
		}

		l := int(opuslen)
		if minLen == 0 || l < minLen {
			minLen = l
		}
		if l > maxLen {
			maxLen = l
		}
		totalBytes += l

		// append encoded pcm data to the buffer
		s.buffer = append(s.buffer, InBuf)
	}

	slog.Debug("dca loaded",
		"path", path,
		"frames", len(s.buffer),
		"bytes", totalBytes,
		"minFrame", minLen,
		"maxFrame", maxLen)
	return nil
}
