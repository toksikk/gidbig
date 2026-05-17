package util

import (
	"testing"

	"github.com/bwmarrin/discordgo"
)

func TestResolveMentions_NoMentions(t *testing.T) {
	s, _ := discordgo.New("Bot fake-token")
	got := ResolveMentions(s, "guild-1", "esoterischer Unsinn über Kristalle")
	if got != "esoterischer Unsinn über Kristalle" {
		t.Fatalf("expected unchanged text, got %q", got)
	}
}

func TestResolveMentions_EmptyText(t *testing.T) {
	s, _ := discordgo.New("Bot fake-token")
	if got := ResolveMentions(s, "guild-1", ""); got != "" {
		t.Fatalf("expected empty string, got %q", got)
	}
}

func TestResolveMentions_UnknownUser_FallsBack(t *testing.T) {
	// Fake session: GuildMember and User both fail → fallback name.
	s, _ := discordgo.New("Bot fake-token")
	got := ResolveMentions(s, "guild-1", "Unsinn über <@123456789>")
	if got == "Unsinn über <@123456789>" {
		t.Fatal("raw mention token should have been replaced")
	}
	if got != "Unsinn über Unbekannter Benutzer" {
		t.Fatalf("expected fallback name, got %q", got)
	}
}

func TestResolveMentions_NicknameFormat(t *testing.T) {
	s, _ := discordgo.New("Bot fake-token")
	got := ResolveMentions(s, "guild-1", "Unsinn über <@!987654321>")
	if got == "Unsinn über <@!987654321>" {
		t.Fatal("nickname-format mention token should have been replaced")
	}
}

func TestResolveMentions_DuplicateMention_ResolvedOnce(t *testing.T) {
	s, _ := discordgo.New("Bot fake-token")
	text := "<@111> und <@111>"
	got := ResolveMentions(s, "guild-1", text)
	if got == text {
		t.Fatal("mentions should have been replaced")
	}
	// Both occurrences should be resolved to the same name.
	expected := "Unbekannter Benutzer und Unbekannter Benutzer"
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}
