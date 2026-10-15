package util

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
)

func TestViewableMemberNamesFiltersByPermission(t *testing.T) {
	members := []*discordgo.Member{
		{User: &discordgo.User{ID: "1", Username: "alice"}},
		{User: &discordgo.User{ID: "2", Username: "bob"}},
		{User: &discordgo.User{ID: "3", Username: "carol"}},
		{User: nil},
		nil,
	}
	permissions := func(userID string) (int64, error) {
		switch userID {
		case "1":
			return discordgo.PermissionViewChannel, nil
		case "2":
			return 0, nil
		case "3":
			return 0, errors.New("boom")
		}
		return 0, nil
	}

	got := viewableMemberNames(members, permissions)
	if len(got) != 1 || got[0] != "alice" {
		t.Fatalf("viewableMemberNames = %v, want [alice]", got)
	}
}

// guildMemberPage builds a JSON member-list response of n members starting at id.
func guildMemberPage(start, n int) string {
	items := make([]map[string]any, 0, n)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("%05d", start+i)
		items = append(items, map[string]any{
			"user": map[string]any{"id": id, "username": "u" + id},
		})
	}
	b, _ := json.Marshal(items)
	return string(b)
}

func TestListGuildMembersPaginates(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("after") == "" {
			_, _ = w.Write([]byte(guildMemberPage(0, 1000)))
			return
		}
		_, _ = w.Write([]byte(guildMemberPage(1000, 3)))
	}))
	t.Cleanup(server.Close)

	origGuilds := discordgo.EndpointGuilds
	discordgo.EndpointGuilds = server.URL + "/guilds/"
	t.Cleanup(func() { discordgo.EndpointGuilds = origGuilds })

	s, err := discordgo.New("Bot test")
	if err != nil {
		t.Fatalf("discordgo.New: %v", err)
	}

	got, err := listGuildMembers(s, "guild-1")
	if err != nil {
		t.Fatalf("listGuildMembers: %v", err)
	}
	if len(got) != 1003 {
		t.Fatalf("members = %d, want 1003", len(got))
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2 (paginated)", requests)
	}
}

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

func TestNoMentions(t *testing.T) {
	got := NoMentions()
	if got == nil || got.Parse == nil || len(got.Parse) != 0 {
		t.Fatalf("NoMentions() = %+v, want empty non-nil Parse", got)
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(raw), `"parse":[]`) {
		t.Errorf("NoMentions() JSON = %s, want \"parse\":[]", raw)
	}
	if NoMentions() == got {
		t.Error("NoMentions() returned a shared pointer")
	}
}
