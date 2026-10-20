package llm

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// TestPersonality_OverridePrecedence pins db custom > yaml custom > db preset >
// yaml preset > default, resolved per call.
func TestPersonality_OverridePrecedence(t *testing.T) {
	none := override("", false)
	tests := []struct {
		name           string
		bootCustom     string
		bootPreset     string
		custom, preset func() (string, bool)
		want, wantSrc  string
	}{
		{"default", "", "", none, none, defaultPersonality, "default"},
		{"yaml preset", "", "hal", none, none, PersonalityPresets["hal"], "preset:hal"},
		{"db preset beats yaml preset", "", "hal", none, override("dry", true), PersonalityPresets["dry"], "preset:dry"},
		{"yaml custom beats db preset", "be a pirate", "", none, override("dry", true), "be a pirate", "custom"},
		{"db custom beats yaml custom", "be a pirate", "hal", override("be a robot", true), override("dry", true), "be a robot", "custom"},
		{"empty override is no override", "be a pirate", "", override("  ", true), none, "be a pirate", "custom"},
		{"unknown db preset falls back to default", "", "hal", none, override("nope", true), defaultPersonality, "default"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Client{customPersonality: tt.bootCustom, preset: tt.bootPreset, personalityOverride: tt.custom, presetOverride: tt.preset}
			if got := c.Personality(); got != tt.want {
				t.Errorf("Personality() = %q, want %q", got, tt.want)
			}
			if got := c.Info().PersonalitySource; got != tt.wantSrc {
				t.Errorf("PersonalitySource = %q, want %q", got, tt.wantSrc)
			}
		})
	}
}

// TestPersonality_OverrideSetThenCleared proves a runtime change applies to the
// next call and that clearing it reverts to the boot persona.
func TestPersonality_OverrideSetThenCleared(t *testing.T) {
	val, ok := "", false
	c := &Client{preset: "hal", presetOverride: func() (string, bool) { return val, ok }}
	if src := c.PersonalitySource(); src != "preset:hal" {
		t.Fatalf("boot source = %q", src)
	}
	val, ok = "dry", true
	if got := c.Personality(); got != PersonalityPresets["dry"] {
		t.Errorf("after override Personality() = %q", got)
	}
	val, ok = "", false
	if src := c.PersonalitySource(); src != "preset:hal" {
		t.Errorf("after clear source = %q, want preset:hal", src)
	}
}

func TestValidatePreset(t *testing.T) {
	for _, ok := range []string{"", "hal", "dry", "schemer", "genalpha"} {
		if err := ValidatePreset(ok); err != nil {
			t.Errorf("ValidatePreset(%q) = %v, want nil", ok, err)
		}
	}
	err := ValidatePreset("nope")
	if err == nil {
		t.Fatal("ValidatePreset(nope) = nil, want an error")
	}
	if !strings.Contains(err.Error(), "dry, genalpha, hal, schemer") {
		t.Errorf("error %q does not list the valid presets", err)
	}
}

// TestPersonality_NoLogPerCall proves the per-request resolution is silent; the
// log lines belong to boot only.
func TestPersonality_NoLogPerCall(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	c := &Client{preset: "nope", personalityOverride: override("x", true)}
	for range 3 {
		_ = c.Personality()
		_ = c.Info()
	}
	if buf.Len() != 0 {
		t.Errorf("per-call resolution logged: %s", buf.String())
	}
}
