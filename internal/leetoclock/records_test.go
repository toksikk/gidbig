package leetoclock

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/leetoclock/util/datastore"
)

func TestRecordCommandsSchema(t *testing.T) {
	command := New().Commands()[0]
	if command.Name != "leetoclock" || len(command.Options) != 2 || command.Options[0].Name != "top" || command.Options[1].Name != "player" {
		t.Fatalf("unexpected command: %#v", command)
	}
	for index, want := range [][]string{{"period", "public"}, {"user", "period", "scope", "public"}} {
		sub := command.Options[index]
		if sub.Type != discordgo.ApplicationCommandOptionSubCommand || len(sub.Options) != len(want) {
			t.Fatalf("subcommand = %#v", sub)
		}
		for j, name := range want {
			if sub.Options[j].Name != name || sub.Options[j].Required {
				t.Fatalf("option %d = %#v", j, sub.Options[j])
			}
		}
	}
	for _, choice := range []string{"month", "7d", "30d", "all"} {
		found := false
		for _, opt := range command.Options[0].Options[0].Choices {
			if opt.Value == choice {
				found = true
			}
		}
		if !found {
			t.Errorf("missing period %q", choice)
		}
	}
}

func recordInteraction(sub, guild, user string, options ...*discordgo.ApplicationCommandInteractionDataOption) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		ID: "interaction", AppID: "app", Token: "token", GuildID: guild, ChannelID: "invoked-channel",
		Type:   discordgo.InteractionApplicationCommand,
		Member: &discordgo.Member{User: &discordgo.User{ID: user}},
		Data: discordgo.ApplicationCommandInteractionData{Name: "leetoclock", Options: []*discordgo.ApplicationCommandInteractionDataOption{{
			Type: discordgo.ApplicationCommandOptionSubCommand, Name: sub, Options: options,
		}}},
	}}
}

func recordOption(name string, value any) *discordgo.ApplicationCommandInteractionDataOption {
	return &discordgo.ApplicationCommandInteractionDataOption{Name: name, Value: value}
}

func addRecord(t *testing.T, m *Module, guild, channel, user, message string, date time.Time, score int) {
	t.Helper()
	season, err := m.store.EnsureSeason(date)
	if err != nil {
		t.Fatal(err)
	}
	game, err := m.store.EnsureGame(channel, guild, date, season.ID)
	if err != nil {
		t.Fatal(err)
	}
	player, err := m.store.EnsurePlayer(user)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.store.CreateScore(message, player.ID, score, game.ID); err != nil {
		t.Fatal(err)
	}
}

type discordCall struct {
	method string
	body   []byte
}

func recordSession(t *testing.T) (*discordgo.Session, <-chan discordCall) {
	t.Helper()
	calls := make(chan discordCall, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		calls <- discordCall{r.Method, body}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(server.Close)
	api, webhooks := discordgo.EndpointAPI, discordgo.EndpointWebhooks
	discordgo.EndpointAPI, discordgo.EndpointWebhooks = server.URL+"/", server.URL+"/webhooks/"
	t.Cleanup(func() { discordgo.EndpointAPI, discordgo.EndpointWebhooks = api, webhooks })
	s, err := discordgo.New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	return s, calls
}

func readCall(t *testing.T, calls <-chan discordCall) discordCall {
	t.Helper()
	select {
	case call := <-calls:
		return call
	case <-time.After(time.Second):
		t.Fatal("missing Discord request")
		return discordCall{}
	}
}

func TestRecordInteractionRoutingAndVisibility(t *testing.T) {
	m, _ := newTestModule(t)
	m.now = func() time.Time { return time.Date(2026, 9, 24, 13, 0, 0, 0, time.Local) }
	addRecord(t, m, "one", "channel", "alice", "msg1", m.now().Add(-time.Hour), 0)
	addRecord(t, m, "one", "channel2", "bob", "msg2", m.now().Add(-time.Hour), 100)
	addRecord(t, m, "two", "elsewhere", "alice", "msg3", m.now().Add(-time.Hour), 1)
	addRecord(t, m, "one", "channel", "alice", "early", m.now().Add(-time.Hour), -10)
	s, calls := recordSession(t)
	for _, tc := range []struct {
		sub          string
		options      []*discordgo.ApplicationCommandInteractionDataOption
		flags        discordgo.MessageFlags
		want, absent string
	}{
		{"top", nil, discordgo.MessageFlagsEphemeral, "<@bob>", "elsewhere"},
		{"top", []*discordgo.ApplicationCommandInteractionDataOption{recordOption("period", "all"), recordOption("public", true)}, 0, "<@alice>", "msg3"},
		{"player", nil, discordgo.MessageFlagsEphemeral, "Valid attempts: 1", "msg3"},
		{"player", []*discordgo.ApplicationCommandInteractionDataOption{recordOption("user", "bob"), recordOption("scope", "global"), recordOption("public", true)}, 0, "<@bob>", "discord.com/channels"},
		{"player", []*discordgo.ApplicationCommandInteractionDataOption{recordOption("scope", "global")}, discordgo.MessageFlagsEphemeral, "Valid attempts: 2", "discord.com/channels"},
	} {
		m.onInteractionCreate(s, recordInteraction(tc.sub, "one", "alice", tc.options...))
		first, second := readCall(t, calls), readCall(t, calls)
		var deferred discordgo.InteractionResponse
		if err := json.Unmarshal(first.body, &deferred); err != nil {
			t.Fatal(err)
		}
		if first.method != http.MethodPost || deferred.Type != discordgo.InteractionResponseDeferredChannelMessageWithSource || deferred.Data.Flags != tc.flags {
			t.Errorf("defer %s: %#v", tc.sub, deferred)
		}
		var edited struct {
			Content         string `json:"content"`
			AllowedMentions struct {
				Parse []string `json:"parse"`
			} `json:"allowed_mentions"`
		}
		if err := json.Unmarshal(second.body, &edited); err != nil {
			t.Fatal(err)
		}
		if second.method != http.MethodPatch || !strings.Contains(edited.Content, tc.want) || strings.Contains(edited.Content, tc.absent) || len(edited.Content) > 2000 || edited.AllowedMentions.Parse == nil || len(edited.AllowedMentions.Parse) != 0 {
			t.Errorf("edit %s: %s %s", tc.sub, second.method, string(second.body))
		}
	}
	// Unrelated interaction types and commands never reach the API.
	i := recordInteraction("top", "one", "alice")
	i.Type = discordgo.InteractionMessageComponent
	m.onInteractionCreate(s, i)
	i.Type = discordgo.InteractionApplicationCommand
	i.Data = discordgo.ApplicationCommandInteractionData{Name: "other"}
	m.onInteractionCreate(s, i)
	select {
	case call := <-calls:
		t.Fatalf("unexpected request: %s", call.body)
	default:
	}
}

func TestRecordErrorsAndNoScores(t *testing.T) {
	m, _ := newTestModule(t)
	m.now = func() time.Time { return time.Date(2026, 9, 24, 13, 0, 0, 0, time.Local) }
	addRecord(t, m, "one", "channel", "alice", "early", m.now().Add(-time.Hour), -5)
	s, calls := recordSession(t)
	for _, tc := range []struct {
		interaction *discordgo.InteractionCreate
		want        string
		deferred    bool
	}{
		{recordInteraction("top", "", "alice", recordOption("public", true)), "Use this command", false},
		{recordInteraction("top", "one", "alice", recordOption("period", "bad"), recordOption("public", true)), "Invalid record options", false},
		{recordInteraction("player", "one", "unknown"), "No stored player", true},
		{recordInteraction("player", "one", "alice"), "No valid scores", true},
		{recordInteraction("top", "one", "alice"), "No valid scores", true},
	} {
		m.onInteractionCreate(s, tc.interaction)
		response := readCall(t, calls)
		var initial discordgo.InteractionResponse
		if err := json.Unmarshal(response.body, &initial); err != nil {
			t.Fatal(err)
		}
		if initial.Data.Flags != discordgo.MessageFlagsEphemeral {
			t.Errorf("error/empty response not private: %s", response.body)
		}
		if tc.deferred {
			response = readCall(t, calls)
		}
		if !strings.Contains(string(response.body), tc.want) {
			t.Errorf("response %s lacks %s", response.body, tc.want)
		}
	}
	// Query failures yield bounded generic text, without raw database errors.
	if err := m.store.Close(); err != nil {
		t.Fatal(err)
	}
	m.onInteractionCreate(s, recordInteraction("top", "one", "alice", recordOption("public", true)))
	_ = readCall(t, calls)
	if response := readCall(t, calls); !strings.Contains(string(response.body), "Could not load records") || strings.Contains(string(response.body), "database is closed") {
		t.Errorf("query failure: %s", response.body)
	}
}

func TestRecordFormattingBounded(t *testing.T) {
	long := strings.Repeat("1", 400)
	rows := make([]datastore.ScoreRecord, 10)
	for i := range rows {
		rows[i] = datastore.ScoreRecord{UserID: long, GuildID: long, ChannelID: long, MessageID: long, Score: i, GameDate: time.Now()}
	}
	if body := renderTop("guild", "all", rows); len(body) > 2000 {
		t.Fatalf("top length %d", len(body))
	}
	if body := renderPlayer(recordRequest{userID: "alice", scope: "global", periodName: "all"}, rows, 10, false); len(body) > 2000 || strings.Contains(body, "discord.com/channels") {
		t.Fatalf("global length/link: %d %s", len(body), body)
	}
}

func TestRecordPeriodsAndGuildScope(t *testing.T) {
	m, _ := newTestModule(t)
	now := time.Date(2026, time.September, 24, 13, 0, 0, 0, time.Local)
	m.now = func() time.Time { return now }
	addRecord(t, m, "one", "recent", "alice", "recent", now.Add(-time.Hour), 1)
	addRecord(t, m, "one", "week", "alice", "week", now.Add(-6*24*time.Hour), 2)
	addRecord(t, m, "one", "month", "alice", "month", now.Add(-20*24*time.Hour), 3)
	addRecord(t, m, "one", "older", "alice", "older", now.Add(-40*24*time.Hour), 4)
	addRecord(t, m, "two", "other", "alice", "other", now.Add(-time.Hour), 0)
	for _, tc := range []struct {
		period string
		server int64
		global int64
	}{
		{"month", 3, 4}, {"7d", 2, 3}, {"30d", 3, 4}, {"all", 4, 5},
	} {
		for _, scope := range []struct {
			name  string
			count int64
		}{{"server", tc.server}, {"global", tc.global}} {
			r, err := parseRecordRequest(recordInteraction("player", "one", "alice", recordOption("period", tc.period), recordOption("scope", scope.name)))
			if err != nil {
				t.Fatal(err)
			}
			body, err := m.recordResponse("one", r)
			if err != nil || !strings.Contains(body, fmt.Sprintf("Valid attempts: %d", scope.count)) {
				t.Errorf("%s/%s: %s, %v", tc.period, scope.name, body, err)
			}
			if scope.name == "global" && (!strings.Contains(body, "server two") || strings.Contains(body, "discord.com/channels")) {
				t.Errorf("global output: %s", body)
			}
		}
	}
	top, err := m.recordResponse("one", recordRequest{subcommand: "top", period: datastore.PeriodMonth, periodName: "month"})
	if err != nil || strings.Contains(top, "server two") || !strings.Contains(top, "1 ms") || strings.Contains(top, "0 ms") {
		t.Errorf("server top: %s, %v", top, err)
	}
}
