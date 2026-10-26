package gippity

import (
	"strings"
	"testing"
)

func TestPrivacyEnabled(t *testing.T) {
	var nilMod *Module
	if !nilMod.PrivacyEnabled("u") {
		t.Error("nil module must fail closed (privacy on)")
	}
	m, _ := setupGippityTest(t)
	if !m.PrivacyEnabled("u1") {
		t.Error("default must be privacy on")
	}
	if err := m.setUserPrivacy("u1", false); err != nil {
		t.Fatalf("setUserPrivacy: %v", err)
	}
	if m.PrivacyEnabled("u1") {
		t.Error("expected privacy off after opt-out")
	}
}

func TestPrivacyCommandMentionsTrivia(t *testing.T) {
	m := New()
	for _, cmd := range m.Commands() {
		for _, sub := range cmd.Options {
			if sub.Name != "privacy" {
				continue
			}
			if !strings.Contains(sub.Description, "trivia") || !strings.Contains(sub.Options[0].Description, "trivia") {
				t.Fatalf("privacy command must disclose trivia opt-in: %q / %q", sub.Description, sub.Options[0].Description)
			}
			if len(sub.Description) > 100 || len(sub.Options[0].Description) > 100 {
				t.Fatal("description exceeds Discord's 100-character limit")
			}
			return
		}
	}
	t.Fatal("privacy subcommand not found")
}
