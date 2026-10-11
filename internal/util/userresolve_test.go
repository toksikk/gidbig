package util

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bwmarrin/discordgo"
)

func TestNormalizeNameFoldsLeetAndSeparators(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Sikk", "sikk"},
		{"s1kk", "sikk"},
		{"s_i-k.k", "sikk"},
		{"1337clock", "ieetclock"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := normalizeName(tc.in); got != tc.want {
			t.Errorf("normalizeName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestMatchRankOrdersExactBeforeFuzzy(t *testing.T) {
	cases := []struct {
		query, field string
		want         int
	}{
		{"sikk", "Sikk", rankExact},
		{"s1kk", "sikk", rankNormalized},
		{"sik", "sikk", rankPrefix},
		{"sikkthetrader", "sikk", rankPrefix},
		{"trader", "sikkthetrader", rankContains},
		{"zzz", "sikk", rankNone},
		{"", "sikk", rankNone},
		{"s", "sikk", rankNone}, // single letters never prefix-match
	}
	for _, tc := range cases {
		if got := matchRank(tc.query, tc.field); got != tc.want {
			t.Errorf("matchRank(%q, %q) = %d, want %d", tc.query, tc.field, got, tc.want)
		}
	}
}

func member(id, username, nick string) *discordgo.Member {
	return &discordgo.Member{Nick: nick, User: &discordgo.User{ID: id, Username: username}}
}

func TestRankMembersFiltersAndOrders(t *testing.T) {
	members := []*discordgo.Member{
		member("1", "sikkthetrader", ""),
		member("2", "sikk", ""),
		member("3", "someoneelse", "sikk"),
		member("4", "unrelated", ""),
		nil,
		{User: nil},
	}

	got := rankMembers(members, "sikk")
	order := make([]string, 0, len(got))
	for _, match := range got {
		order = append(order, match.ID)
	}
	// "sikk" (exact) first, then the two normalized-exact/prefix matches by label.
	if len(order) != 3 || order[0] != "2" {
		t.Fatalf("ranked IDs = %v, want 3 results with ID 2 first", order)
	}
	for _, id := range order {
		if id == "4" {
			t.Errorf("unrelated member leaked into results: %v", order)
		}
	}
}

func TestRankMembersLeetQuery(t *testing.T) {
	members := []*discordgo.Member{member("1", "sikk", "")}
	got := rankMembers(members, "s1kk")
	if len(got) != 1 || got[0].ID != "1" {
		t.Fatalf("rankMembers leet = %v, want the sikk member", got)
	}
}

// stubGuildMembers points the List Guild Members endpoint at a test server that
// returns the given members JSON.
func stubGuildMembers(t *testing.T, body string) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)

	orig := discordgo.EndpointGuilds
	discordgo.EndpointGuilds = server.URL + "/guilds/"
	t.Cleanup(func() { discordgo.EndpointGuilds = orig })
}

func newStateSession(t *testing.T, guildID string, members ...*discordgo.Member) *discordgo.Session {
	t.Helper()
	s, err := discordgo.New("Bot test")
	if err != nil {
		t.Fatalf("discordgo.New: %v", err)
	}
	if err := s.State.GuildAdd(&discordgo.Guild{ID: guildID}); err != nil {
		t.Fatalf("GuildAdd: %v", err)
	}
	for _, m := range members {
		if m.GuildID == "" {
			m.GuildID = guildID
		}
		if err := s.State.MemberAdd(m); err != nil {
			t.Fatalf("MemberAdd: %v", err)
		}
	}
	return s
}

func TestResolveMemberQueryUniqueFromState(t *testing.T) {
	stubGuildMembers(t, "[]")
	session := newStateSession(t, "guild-1", member("42", "sikk", ""))

	match, ok, note := ResolveMemberQuery(session, "guild-1", "s1kk")
	if !ok {
		t.Fatalf("ok = false, note = %q", note)
	}
	if match.ID != "42" || match.Label() != "sikk" {
		t.Fatalf("match = %+v, want ID 42 label sikk", match)
	}
}

func TestResolveMemberQueryPassesThroughUserID(t *testing.T) {
	match, ok, note := ResolveMemberQuery(nil, "", "123456789012345678")
	if !ok || match.ID != "123456789012345678" || note != "" {
		t.Fatalf("ID passthrough = (%+v, %v, %q)", match, ok, note)
	}
}

func TestResolveMemberQueryResolvesMention(t *testing.T) {
	match, ok, _ := ResolveMemberQuery(nil, "", "<@!123456789012345678>")
	if !ok || match.ID != "123456789012345678" {
		t.Fatalf("mention = (%+v, %v)", match, ok)
	}
}

func TestResolveMemberQueryAmbiguous(t *testing.T) {
	stubGuildMembers(t, "[]")
	session := newStateSession(t, "guild-1", member("1", "sikk", ""), member("2", "sikkmode", ""))

	match, ok, note := ResolveMemberQuery(session, "guild-1", "sikk")
	if ok {
		t.Fatalf("expected ambiguity, got %+v", match)
	}
	if note == "" {
		t.Fatal("expected a candidate-list note")
	}
}

func TestResolveMemberQueryNotFound(t *testing.T) {
	stubGuildMembers(t, "[]")
	session := newStateSession(t, "guild-1", member("1", "alice", ""))

	_, ok, note := ResolveMemberQuery(session, "guild-1", "nobody")
	if ok {
		t.Fatal("expected ok=false")
	}
	if note == "" {
		t.Fatal("expected a not-found note")
	}
}

func TestResolveMemberUsesRESTWhenStateEmpty(t *testing.T) {
	stubGuildMembers(t, `[{"user":{"id":"7","username":"s1kk"}}]`)
	session, err := discordgo.New("Bot test")
	if err != nil {
		t.Fatalf("discordgo.New: %v", err)
	}

	matches, err := ResolveMember(session, "guild-1", "sikk")
	if err != nil {
		t.Fatalf("ResolveMember: %v", err)
	}
	if len(matches) != 1 || matches[0].ID != "7" {
		t.Fatalf("matches = %+v, want REST member 7", matches)
	}
}

func TestResolveMemberFallsBackToStateOnRESTError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(server.Close)
	orig := discordgo.EndpointGuilds
	discordgo.EndpointGuilds = server.URL + "/guilds/"
	t.Cleanup(func() { discordgo.EndpointGuilds = orig })

	session := newStateSession(t, "guild-1", member("9", "sikk", ""))
	matches, err := ResolveMember(session, "guild-1", "sikk")
	if err != nil {
		t.Fatalf("ResolveMember: %v", err)
	}
	if len(matches) != 1 || matches[0].ID != "9" {
		t.Fatalf("matches = %+v, want state member 9", matches)
	}
}

func TestResolveMemberErrorsWhenBothFail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(server.Close)
	orig := discordgo.EndpointGuilds
	discordgo.EndpointGuilds = server.URL + "/guilds/"
	t.Cleanup(func() { discordgo.EndpointGuilds = orig })

	session, err := discordgo.New("Bot test")
	if err != nil {
		t.Fatalf("discordgo.New: %v", err)
	}
	if _, err := ResolveMember(session, "guild-1", "sikk"); err == nil {
		t.Fatal("expected an error when state and REST both fail")
	}
}
