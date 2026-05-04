package gidbig

import (
	"bytes"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
)

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
	}
	if got.Name != "only" {
		t.Errorf("Name = %q, want %q", got.Name, "only")
	}
}

func TestBanner_WritesToWriter(t *testing.T) {
	var buf bytes.Buffer
	Banner(&buf, nil)

	output := buf.String()
	if len(output) == 0 {
		t.Error("Banner() wrote nothing to writer")
	}
	if !strings.Contains(output, "gidbig") {
		t.Errorf("Banner() output does not contain 'gidbig': %q", output)
	}
}

func TestBanner_WithPlugins(t *testing.T) {
	var buf bytes.Buffer
	plugins := map[string][2]string{
		"test-plugin": {"1.0.0", "2024-01-01"},
	}
	Banner(&buf, plugins)

	output := buf.String()
	if !strings.Contains(output, "test-plugin") {
		t.Errorf("Banner() output missing plugin name %q\noutput: %s", "test-plugin", output)
	}
	if !strings.Contains(output, "1.0.0") {
		t.Errorf("Banner() output missing plugin version\noutput: %s", output)
	}
}

func TestBanner_NilWriter(t *testing.T) {
	// Should not panic when writer is nil (writes to stdout)
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Banner(nil, nil) panicked: %v", r)
		}
	}()
	Banner(nil, nil)
}

func TestAddNewSoundCollection(t *testing.T) {
	originalCollections := COLLECTIONS
	COLLECTIONS = nil
	t.Cleanup(func() { COLLECTIONS = originalCollections })

	addNewSoundCollection("test", "sound1")

	if len(COLLECTIONS) != 1 {
		t.Fatalf("expected 1 collection, got %d", len(COLLECTIONS))
	}
	if COLLECTIONS[0].Prefix != "test" {
		t.Errorf("Prefix = %q, want %q", COLLECTIONS[0].Prefix, "test")
	}
	if len(COLLECTIONS[0].Commands) != 1 || COLLECTIONS[0].Commands[0] != "!test" {
		t.Errorf("Commands = %v, want [!test]", COLLECTIONS[0].Commands)
	}
	if len(COLLECTIONS[0].Sounds) != 1 || COLLECTIONS[0].Sounds[0].Name != "sound1" {
		t.Errorf("Sounds[0].Name = %q, want %q", COLLECTIONS[0].Sounds[0].Name, "sound1")
	}
}

func TestStatusInteractionResponse_Owner(t *testing.T) {
	ownerID := "owner123"
	statsOutput := "some stats"

	resp := statusInteractionResponse(ownerID, ownerID, func() string { return statsOutput })

	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if resp.Type != discordgo.InteractionResponseChannelMessageWithSource {
		t.Errorf("Type = %v, want InteractionResponseChannelMessageWithSource", resp.Type)
	}
	if resp.Data.Flags != discordgo.MessageFlagsEphemeral {
		t.Errorf("Flags = %v, want Ephemeral", resp.Data.Flags)
	}
	if !strings.Contains(resp.Data.Content, statsOutput) {
		t.Errorf("Content %q does not contain stats output %q", resp.Data.Content, statsOutput)
	}
	if !strings.HasPrefix(resp.Data.Content, "```") || !strings.HasSuffix(resp.Data.Content, "```") {
		t.Errorf("Content %q not wrapped in code block", resp.Data.Content)
	}
}

func TestStatusInteractionResponse_NonOwner(t *testing.T) {
	ownerID := "owner123"
	callerID := "rando456"

	called := false
	resp := statusInteractionResponse(callerID, ownerID, func() string {
		called = true
		return "stats"
	})

	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if called {
		t.Error("buildStats should not be called for non-owner")
	}
	if resp.Data.Flags != discordgo.MessageFlagsEphemeral {
		t.Errorf("Flags = %v, want Ephemeral", resp.Data.Flags)
	}
	if resp.Data.Content != "Access denied." {
		t.Errorf("Content = %q, want %q", resp.Data.Content, "Access denied.")
	}
}
