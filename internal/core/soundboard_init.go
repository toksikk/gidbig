package gidbig

import (
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
)

// COLLECTIONS all collections
var COLLECTIONS []*soundCollection

// Create collections
func createCollections() {
	files, _ := os.ReadDir("./audio")
	for _, f := range files {
		if strings.Contains(f.Name(), ".dca") {
			soundfile := strings.Split(strings.Replace(f.Name(), ".dca", "", -1), "_")
			containsPrefix := false
			containsSound := false

			if len(COLLECTIONS) == 0 {
				addNewSoundCollection(soundfile[0], soundfile[1])
			}
			for _, c := range COLLECTIONS {
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
				addNewSoundCollection(soundfile[0], soundfile[1])
			}
		}
	}
}

func addNewSoundCollection(prefix string, soundname string) {
	var SC = &soundCollection{
		Prefix: prefix,
		Commands: []string{
			"!" + prefix,
		},
		Sounds: []*soundClip{
			createSound(soundname, 1, 250),
		},
	}
	COLLECTIONS = append(COLLECTIONS, SC)
}

// Create a Sound struct
func createSound(Name string, Weight int, PartDelay int) *soundClip {
	return &soundClip{
		Name:      Name,
		Weight:    Weight,
		PartDelay: PartDelay,
		buffer:    make([][]byte, 0),
	}
}

// Load soundcollection
func (sc *soundCollection) Load() {
	for _, sound := range sc.Sounds {
		sc.soundRange += sound.Weight
		err := sound.Load(sc)
		if err != nil {
			slog.Error("error adding sound to soundCollection", "Error", err)
		}
	}
}

// Load attempts to load an encoded sound file from disk
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

	var opuslen int16

	for {
		// read opus frame length from dca file
		err = binary.Read(file, binary.LittleEndian, &opuslen)

		// If this is the end of the file, just return
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return nil
		}

		if err != nil {
			slog.Error("error reading from dca file", "error", err)
			return err
		}

		// read encoded pcm from dca file
		InBuf := make([]byte, opuslen)
		err = binary.Read(file, binary.LittleEndian, &InBuf)

		// Should not be any end of file errors
		if err != nil {
			slog.Error("error reading from dca file", "error", err)
			return err
		}

		// append encoded pcm data to the buffer
		s.buffer = append(s.buffer, InBuf)
	}
}
