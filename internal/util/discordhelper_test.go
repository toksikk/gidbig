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

func TestResolveMentionsWithRestore_NoMentions_NoopRestore(t *testing.T) {
	s, _ := discordgo.New("Bot fake-token")
	resolved, restore := ResolveMentionsWithRestore(s, "guild-1", "kein Mention hier")
	if resolved != "kein Mention hier" {
		t.Fatalf("expected unchanged text, got %q", resolved)
	}
	if got := restore("some generated text"); got != "some generated text" {
		t.Fatalf("no-op restore changed text: %q", got)
	}
}

func TestResolveMentionsWithRestore_RestoresToken(t *testing.T) {
	s, _ := discordgo.New("Bot fake-token")
	// Fake session: unknown user → "Unbekannter Benutzer"
	_, restore := ResolveMentionsWithRestore(s, "guild-1", "<@123456789>")
	generated := "Die Energie von Unbekannter Benutzer ist stark."
	got := restore(generated)
	if got != "Die Energie von <@123456789> ist stark." {
		t.Fatalf("unexpected restored text: %q", got)
	}
}

func TestResolveMentionsWithRestore_ResolveThenRestore_RoundTrip(t *testing.T) {
	s, _ := discordgo.New("Bot fake-token")
	input := "Unsinn über <@999>"
	resolved, restore := ResolveMentionsWithRestore(s, "guild-1", input)
	if resolved == input {
		t.Fatal("resolve should have replaced the mention token")
	}
	restored := restore(resolved)
	if restored != input {
		t.Fatalf("round-trip failed: got %q, want %q", restored, input)
	}
}
