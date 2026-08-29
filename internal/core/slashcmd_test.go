package gidbig

import (
	"strings"
	"testing"
)

func TestBuildListMessage_TruncatesLongOutput(t *testing.T) {
	orig := COLLECTIONS
	COLLECTIONS = make([]*soundCollection, 0, 100)
	for i := 0; i < 100; i++ {
		prefix := "coll" + string(rune('A'+i%26))
		for j := 0; j < 10; j++ {
			sname := "sound_" + string(rune('a'+j))
			COLLECTIONS = append(COLLECTIONS, &soundCollection{
				Prefix: prefix,
				Sounds: []*soundClip{{Name: sname}},
			})
		}
	}
	defer func() { COLLECTIONS = orig }()

	out := buildListMessage()
	if len(out) > maxContentLen {
		t.Errorf("buildListMessage returned %d chars, want <= %d", len(out), maxContentLen)
	}
	if !strings.Contains(out, "(remaining collections omitted)") {
		t.Error("expected truncation marker not found")
	}
}

func TestBuildListMessage_FitsUnderLimit(t *testing.T) {
	orig := COLLECTIONS
	fewSounds := []*soundClip{{Name: "a"}, {Name: "b"}}
	bigPrefix := strings.Repeat("X", 500)
	for i := 0; i < 2; i++ {
		prefix := bigPrefix + string(rune('A'+i))
		COLLECTIONS = append(COLLECTIONS, &soundCollection{
			Prefix: prefix,
			Sounds: fewSounds,
		})
	}
	defer func() { COLLECTIONS = orig }()

	out := buildListMessage()
	if len(out) > maxContentLen {
		t.Errorf("buildListMessage returned %d chars, want <= %d", len(out), maxContentLen)
	}
}

func TestSoundCollection_Lookup_CaseInsensitive(t *testing.T) {
	sounds := []*soundClip{{Name: "Airhorn"}, {Name: "Wowee"}}
	sc := &soundCollection{Prefix: "airhorn", Sounds: sounds}

	tests := []struct {
		input    string
		expected string
	}{
		{"airhorn", "Airhorn"},
		{"Airhorn", "Airhorn"},
		{"AIRHORN", "Airhorn"},
		{"wowee", "Wowee"},
		{"WOWEE", "Wowee"},
	}

	for _, tt := range tests {
		result := sc.Lookup(tt.input)
		if tt.expected == "" {
			if result != nil {
				t.Errorf("Lookup(%q) = %q, want nil", tt.input, result.Name)
			}
			continue
		}
		if result == nil || result.Name != tt.expected {
			t.Errorf("Lookup(%q) = %v, want %q", tt.input, result, tt.expected)
		}
	}
}
