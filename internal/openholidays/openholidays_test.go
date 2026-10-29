package openholidays

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/bot"
)

func TestParseQueryDefaults(t *testing.T) {
	m, _ := newTestModule(t)
	q, err := m.parseQuery(rawQuery{}, spanMonths(12))
	if err != nil {
		t.Fatal(err)
	}
	want := query{country: "DE", kind: typePublic, from: "2026-10-25", to: "2027-10-24"}
	if q != want {
		t.Fatalf("q = %+v, want %+v", q, want)
	}
}

func TestParseQueryRegionDerivesCountry(t *testing.T) {
	m, _ := newTestModule(t)
	q, err := m.parseQuery(rawQuery{region: "de-nw", kind: "School"}, spanMonths(12))
	if err != nil || q.country != "DE" || q.region != "DE-NW" || q.kind != typeSchool {
		t.Fatalf("q=%+v err=%v", q, err)
	}
}

func TestParseQueryErrors(t *testing.T) {
	m, _ := newTestModule(t)
	for _, r := range []rawQuery{
		{from: "2026-13-01"},
		{to: "tomorrow"},
		{from: "2026-10-25", to: "2026-10-24"},
		{from: "2026-01-01", to: "2029-01-01"},
		{country: "DEU"},
		{region: "NW"},
		{country: "AT", region: "DE-NW"},
		{kind: "bank"},
	} {
		if _, err := m.parseQuery(r, spanMonths(12)); !errors.Is(err, errInput) {
			t.Errorf("%+v: err = %v", r, err)
		}
	}
	if _, err := m.parseQuery(rawQuery{from: "2026-01-01", to: "2028-12-31"}, spanMonths(12)); err != nil {
		t.Errorf("just under 3 years: %v", err)
	}
}

func TestCheckCountry(t *testing.T) {
	m, f := newTestModule(t)
	ctx := context.Background()
	if err := m.checkCountry(ctx, "AT"); err != nil {
		t.Fatal(err)
	}
	if err := m.checkCountry(ctx, "US"); !errors.Is(err, errInput) {
		t.Fatalf("err = %v", err)
	}
	m2, f2 := newTestModule(t)
	f2.status = http.StatusInternalServerError
	if err := m2.checkCountry(ctx, "US"); err != nil {
		t.Fatalf("api down should skip check, got %v", err)
	}
	if f.metaCalls.Load() != 1 {
		t.Errorf("countries not cached: %d calls", f.metaCalls.Load())
	}
}

func TestLookupCachesAndFilters(t *testing.T) {
	m, f := newTestModule(t)
	q := query{country: "DE", region: "DE-NW", kind: typePublic, from: "2026-10-25", to: "2027-10-24"}
	for range 2 {
		hs, err := m.lookup(context.Background(), q)
		if err != nil {
			t.Fatal(err)
		}
		if len(hs) != 2 || hs[0].ID != "2" || hs[1].ID != "1" {
			t.Fatalf("hs = %+v", hs)
		}
	}
	if f.holidayCalls.Load() != 1 {
		t.Fatalf("calls = %d", f.holidayCalls.Load())
	}
}

func TestComposeDiscordError(t *testing.T) {
	m, f := newTestModule(t)
	f.status = http.StatusInternalServerError
	out := m.composeDiscord(context.Background(), query{country: "DE", kind: typePublic, from: "a", to: "b"})
	if !strings.HasPrefix(out, "Failed to get holidays") {
		t.Fatalf("out = %q", out)
	}
}

func TestCommands(t *testing.T) {
	cmds := New().Commands()
	if len(cmds) != 1 || cmds[0].Name != commandName {
		t.Fatalf("commands = %+v", cmds)
	}
	seen := map[string]bool{}
	for _, o := range cmds[0].Options {
		if seen[o.Name] || o.Required {
			t.Errorf("option %q duplicate or required", o.Name)
		}
		seen[o.Name] = true
	}
	for _, n := range []string{"country", "region", "type", "from", "to"} {
		if !seen[n] {
			t.Errorf("missing option %s", n)
		}
	}
}

func focusedData(name, value string, extra ...*discordgo.ApplicationCommandInteractionDataOption) discordgo.ApplicationCommandInteractionData {
	opts := append([]*discordgo.ApplicationCommandInteractionDataOption{{Name: name, Value: value, Focused: true, Type: discordgo.ApplicationCommandOptionString}}, extra...)
	return discordgo.ApplicationCommandInteractionData{Name: commandName, Options: opts}
}

func TestAutocompleteRegion(t *testing.T) {
	m, _ := newTestModule(t)
	ch := m.autocompleteChoices(context.Background(), focusedData("region", "de-b"))
	if len(ch) != 2 || ch[0].Value != "DE-BY" || ch[0].Name != "DE-BY — Bayern" {
		t.Fatalf("choices = %+v", ch)
	}
	ch = m.autocompleteChoices(context.Background(), focusedData("region", "nord"))
	if len(ch) != 1 || ch[0].Value != "DE-NW" {
		t.Fatalf("by name = %+v", ch)
	}
}

func TestAutocompleteCountryAndLimit(t *testing.T) {
	m, _ := newTestModule(t)
	ch := m.autocompleteChoices(context.Background(), focusedData("country", "ö"))
	if len(ch) != 1 || ch[0].Value != "AT" {
		t.Fatalf("choices = %+v", ch)
	}
	var list []country
	for i := range 40 {
		list = append(list, country{IsoCode: string(rune('A'+i/26)) + string(rune('A'+i%26))})
	}
	m.countries = newFlightCache[[]country](context.Background(), time.Hour, 4, time.Now)
	_, _ = m.countries.get(context.Background(), "countries", func(context.Context) ([]country, error) { return list, nil })
	if ch := m.autocompleteChoices(context.Background(), focusedData("country", "")); len(ch) != autocompleteLimit {
		t.Fatalf("len = %d", len(ch))
	}
}

func TestAutocompleteErrorEmpty(t *testing.T) {
	m, f := newTestModule(t)
	f.status = http.StatusInternalServerError
	ch := m.autocompleteChoices(context.Background(), focusedData("region", ""))
	if ch == nil || len(ch) != 0 {
		t.Fatalf("choices = %+v", ch)
	}
}

type discordRequest struct {
	path string
	body []byte
}

func discordSession(t *testing.T) (*discordgo.Session, <-chan discordRequest) {
	t.Helper()
	requests := make(chan discordRequest, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests <- discordRequest{path: r.URL.Path, body: body}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(server.Close)
	origAPI, origWebhooks := discordgo.EndpointAPI, discordgo.EndpointWebhooks
	discordgo.EndpointAPI = server.URL + "/"
	discordgo.EndpointWebhooks = server.URL + "/webhooks/"
	t.Cleanup(func() {
		discordgo.EndpointAPI = origAPI
		discordgo.EndpointWebhooks = origWebhooks
	})
	s, err := discordgo.New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	return s, requests
}

func interaction(typ discordgo.InteractionType, data discordgo.ApplicationCommandInteractionData) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		ID: "interaction", AppID: "app", Token: "token", Type: typ, ChannelID: "channel", Data: data,
	}}
}

func awaitRequest(t *testing.T, requests <-chan discordRequest) discordRequest {
	t.Helper()
	select {
	case r := <-requests:
		return r
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a Discord request")
		return discordRequest{}
	}
}

var discordMu sync.Mutex // tests swap global discordgo endpoints

func TestCommandValidationEphemeral(t *testing.T) {
	discordMu.Lock()
	defer discordMu.Unlock()
	m, _ := newTestModule(t)
	s, requests := discordSession(t)
	data := discordgo.ApplicationCommandInteractionData{Name: commandName, Options: []*discordgo.ApplicationCommandInteractionDataOption{
		{Name: "from", Type: discordgo.ApplicationCommandOptionString, Value: "2026-01-01"},
		{Name: "to", Type: discordgo.ApplicationCommandOptionString, Value: "2030-01-01"},
	}}
	m.onInteractionCreate(s, interaction(discordgo.InteractionApplicationCommand, data))
	var resp discordgo.InteractionResponse
	if err := json.Unmarshal(awaitRequest(t, requests).body, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Data == nil || resp.Data.Flags&discordgo.MessageFlagsEphemeral == 0 || !strings.Contains(resp.Data.Content, "3 years") {
		t.Fatalf("resp = %+v", resp.Data)
	}
}

func TestCommandDefersThenEdits(t *testing.T) {
	discordMu.Lock()
	defer discordMu.Unlock()
	m, _ := newTestModule(t)
	s, requests := discordSession(t)
	m.onInteractionCreate(s, interaction(discordgo.InteractionApplicationCommand, discordgo.ApplicationCommandInteractionData{Name: commandName}))
	var resp discordgo.InteractionResponse
	if err := json.Unmarshal(awaitRequest(t, requests).body, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Type != discordgo.InteractionResponseDeferredChannelMessageWithSource {
		t.Fatalf("type = %v", resp.Type)
	}
	edit := awaitRequest(t, requests)
	if !strings.Contains(string(edit.body), "Weihnachtstag") {
		t.Fatalf("edit = %s", edit.body)
	}
}

func TestAutocompleteResponds(t *testing.T) {
	discordMu.Lock()
	defer discordMu.Unlock()
	m, _ := newTestModule(t)
	s, requests := discordSession(t)
	m.onInteractionCreate(s, interaction(discordgo.InteractionApplicationCommandAutocomplete, focusedData("region", "DE-N")))
	var resp discordgo.InteractionResponse
	if err := json.Unmarshal(awaitRequest(t, requests).body, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Type != discordgo.InteractionApplicationCommandAutocompleteResult || len(resp.Data.Choices) != 1 {
		t.Fatalf("resp = %+v", resp)
	}
}

func TestCommandUnknownCountryAfterDefer(t *testing.T) {
	discordMu.Lock()
	defer discordMu.Unlock()
	m, _ := newTestModule(t)
	s, requests := discordSession(t)
	data := discordgo.ApplicationCommandInteractionData{Name: commandName, Options: []*discordgo.ApplicationCommandInteractionDataOption{
		{Name: "country", Type: discordgo.ApplicationCommandOptionString, Value: "US"},
	}}
	m.onInteractionCreate(s, interaction(discordgo.InteractionApplicationCommand, data))
	var resp discordgo.InteractionResponse
	if err := json.Unmarshal(awaitRequest(t, requests).body, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Type != discordgo.InteractionResponseDeferredChannelMessageWithSource {
		t.Fatalf("first response type = %v, want deferred", resp.Type)
	}
	if edit := awaitRequest(t, requests); !strings.Contains(string(edit.body), "country US is not supported") {
		t.Fatalf("edit = %s", edit.body)
	}
}

func TestParseQueryTodayInBerlin(t *testing.T) {
	m, _ := newTestModule(t)
	m.now = func() time.Time { return time.Date(2026, 10, 25, 23, 30, 0, 0, time.UTC) }
	q, err := m.parseQuery(rawQuery{}, spanMonths(12))
	if err != nil || q.from != "2026-10-26" {
		t.Fatalf("q=%+v err=%v", q, err)
	}
}

func TestCommandRepliesShuttingDownAfterShutdown(t *testing.T) {
	discordMu.Lock()
	defer discordMu.Unlock()
	m, _ := newTestModule(t)
	s, requests := discordSession(t)
	b := bot.New(bot.Deps{Session: s})
	b.Shutdown()
	m.router = b.Router
	m.onInteractionCreate(s, interaction(discordgo.InteractionApplicationCommand, discordgo.ApplicationCommandInteractionData{Name: commandName}))
	awaitRequest(t, requests) // deferred
	var edit struct {
		Content *string `json:"content"`
	}
	if err := json.Unmarshal(awaitRequest(t, requests).body, &edit); err != nil {
		t.Fatal(err)
	}
	if edit.Content == nil || *edit.Content != bot.MsgShuttingDown {
		t.Fatalf("content = %v, want %q", edit.Content, bot.MsgShuttingDown)
	}
}

func TestCacheTTLAndSingleFlight(t *testing.T) {
	now := time.Now()
	c := newFlightCache[int](context.Background(), time.Minute, 4, func() time.Time { return now })
	n := 0
	var mu sync.Mutex
	fetch := func(context.Context) (int, error) {
		mu.Lock()
		n++
		mu.Unlock()
		time.Sleep(20 * time.Millisecond)
		return 1, nil
	}
	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() { _, _ = c.get(context.Background(), "k", fetch) })
	}
	wg.Wait()
	if n != 1 {
		t.Fatalf("n = %d", n)
	}
	now = now.Add(2 * time.Minute)
	_, _ = c.get(context.Background(), "k", fetch)
	if n != 2 {
		t.Fatalf("after TTL n = %d", n)
	}
	errFetch := func(context.Context) (int, error) { return 0, errors.New("boom") }
	_, _ = c.get(context.Background(), "e", errFetch)
	_, _ = c.get(context.Background(), "e", errFetch)
	if c.stats().errors != 2 {
		t.Fatalf("errors cached: %+v", c.stats())
	}
}
