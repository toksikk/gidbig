package tmdb

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/bot"
	"github.com/toksikk/gidbig/internal/cfg"
	"github.com/toksikk/gidbig/internal/tool"
)

func testModule(base string) *Module {
	m := New()
	m.client = testClient(base)
	return m
}

func TestInit_RegistersCommandComponentAndTool(t *testing.T) {
	conf := &cfg.Config{}
	conf.TMDB.ReadAccessToken = testToken
	tools := tool.NewRegistry()
	b := bot.New(bot.Deps{Config: conf, Tools: tools})
	if err := b.RegisterModule(New()); err != nil {
		t.Fatalf("RegisterModule: %v", err)
	}
	cmds := b.Router.Commands()
	if len(cmds) != 1 || cmds[0].Name != "wheretostream" {
		t.Fatalf("commands = %+v", cmds)
	}
	opts := cmds[0].Options
	if len(opts) != 2 || opts[0].Name != "title" || !opts[0].Required || opts[1].Name != "region" || opts[1].Required {
		t.Errorf("options = %+v", opts)
	}
	if _, ok := tools.Get(toolName); !ok {
		t.Error("streaming_providers tool not registered")
	}
}

func TestInit_RejectsEmptyToken(t *testing.T) {
	if err := New().Init(bot.Deps{Config: &cfg.Config{}}); err == nil {
		t.Error("Init without token = nil error")
	}
}

func TestNormalizeRegion(t *testing.T) {
	m := New()
	for in, want := range map[string]string{"": "DE", " at ": "AT", "us": "US"} {
		if got, err := m.normalizeRegion(in); err != nil || got != want {
			t.Errorf("normalizeRegion(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"deu", "d", "1A", "ü1"} {
		if _, err := m.normalizeRegion(bad); err == nil {
			t.Errorf("normalizeRegion(%q) = nil error", bad)
		}
	}
	conf := &cfg.Config{}
	conf.TMDB.Region = "CH"
	m.conf = conf
	if got, _ := m.normalizeRegion(""); got != "CH" {
		t.Errorf("config default = %q, want CH", got)
	}
}

func TestParsePickID(t *testing.T) {
	user, region, err := parsePickID(pickCustomID("123456", "DE"))
	if err != nil || user != "123456" || region != "DE" {
		t.Fatalf("roundtrip = %q %q %v", user, region, err)
	}
	for _, bad := range []string{"", "tmdb:pick:", "tmdb:pick:123", "tmdb:pick:abc:DE", "tmdb:pick:123:de", "tmdb:pick:123:", "coffee:123:DE", "tmdb:pick:123:DE:x"} {
		if _, _, err := parsePickID(bad); err == nil {
			t.Errorf("parsePickID(%q) = nil error", bad)
		}
	}
}

func TestParsePickValue(t *testing.T) {
	if kind, id, err := parsePickValue("tv:70523"); err != nil || kind != kindTV || id != 70523 {
		t.Errorf("tv = %q %d %v", kind, id, err)
	}
	if kind, id, err := parsePickValue("movie:1"); err != nil || kind != kindMovie || id != 1 {
		t.Errorf("movie = %q %d %v", kind, id, err)
	}
	for _, bad := range []string{"", "person:1", "movie:", "movie:-1", "movie:0", "movie:12a", "tv:1:2", "https://evil/x", "movie:99999999999999999999"} {
		if _, _, err := parsePickValue(bad); err == nil {
			t.Errorf("parsePickValue(%q) = nil error", bad)
		}
	}
}

func TestPickRefusalIsEphemeral(t *testing.T) {
	i := &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{Member: &discordgo.Member{User: &discordgo.User{ID: "999"}}}}
	if interactionUserID(i) == "123" {
		t.Fatal("other user matched owner")
	}
	r := ephemeralResponse("Not your search")
	if r.Data.Flags&discordgo.MessageFlagsEphemeral == 0 {
		t.Error("refusal is not ephemeral")
	}
	i = &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{User: &discordgo.User{ID: "123"}}}
	if interactionUserID(i) != "123" {
		t.Error("DM user not resolved")
	}
}

func TestSearchReply(t *testing.T) {
	srv := newStub(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Query().Get("query"), "nothing") {
			_, _ = w.Write([]byte(`{"results":[]}`))
			return
		}
		_, _ = w.Write([]byte(multiJSON))
	})
	m := testModule(srv.URL)

	edit := m.searchReply(context.Background(), "Dark", "42", "AT")
	if len(*edit.Components) != 1 {
		t.Fatalf("components = %+v", *edit.Components)
	}
	menu := (*edit.Components)[0].(discordgo.ActionsRow).Components[0].(discordgo.SelectMenu)
	if menu.CustomID != "tmdb:pick:42:AT" || len(menu.Options) != 2 || menu.Options[0].Value != "tv:70523" {
		t.Errorf("menu = %+v", menu)
	}

	edit = m.searchReply(context.Background(), "nothing`", "42", "DE")
	if !strings.Contains(*edit.Content, "No movie or series found for `nothing`") || len(*edit.Components) != 0 {
		t.Errorf("not-found edit = %q %+v", *edit.Content, *edit.Components)
	}
}

func TestDetailsCache(t *testing.T) {
	srv := newStub(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(detailsJSON)) })
	m := testModule(srv.URL)
	now := time.Unix(1000, 0)
	m.now = func() time.Time { return now }
	for range 2 {
		if _, err := m.details(context.Background(), kindTV, 70523); err != nil {
			t.Fatal(err)
		}
	}
	if n := srv.requests.Load(); n != 1 {
		t.Errorf("requests = %d, want 1 (cached)", n)
	}
	now = now.Add(cacheTTL + time.Second)
	if _, err := m.details(context.Background(), kindTV, 70523); err != nil {
		t.Fatal(err)
	}
	if n := srv.requests.Load(); n != 2 {
		t.Errorf("requests after expiry = %d, want 2", n)
	}
}

func TestStreamingTool(t *testing.T) {
	srv := newStub(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/search/") {
			_, _ = w.Write([]byte(`{"results":[{"media_type":"tv","id":70523,"name":"Dark","first_air_date":"2017-12-01"}]}`))
			return
		}
		_, _ = w.Write([]byte(detailsJSON))
	})
	m := testModule(srv.URL)
	m.tools = tool.NewRegistry()
	m.registerTools()
	def, ok := m.tools.Get(toolName)
	if !ok {
		t.Fatal("tool not registered")
	}
	if !json.Valid(def.Parameters) {
		t.Error("schema is not valid JSON")
	}

	out, err := def.Handler(context.Background(), tool.Env{}, json.RawMessage(`{"title":"Dark"}`))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if !strings.Contains(out, "Dark (2017)") || !strings.Contains(out, "Netflix") || !strings.Contains(out, "JustWatch") {
		t.Errorf("output = %q", out)
	}

	if _, err := def.Handler(context.Background(), tool.Env{}, json.RawMessage(`{"title":"Dark","type":"tv","year":2017,"region":"de"}`)); err != nil {
		t.Fatalf("typed handler: %v", err)
	}

	for _, bad := range []string{`{}`, `{"title":" "}`, `{"title":"x","type":"person"}`, `{"title":"x","region":"DEU"}`, `{"title":"x","year":-1}`, `not json`} {
		if _, err := def.Handler(context.Background(), tool.Env{}, json.RawMessage(bad)); err == nil {
			t.Errorf("handler(%s) = nil error", bad)
		}
	}
}

func TestStreamingTool_TypedSearchEndpoint(t *testing.T) {
	var paths []string
	srv := newStub(t, func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path+"?"+r.URL.RawQuery)
		_, _ = w.Write([]byte(`{"results":[]}`))
	})
	m := testModule(srv.URL)
	out, err := m.streamingToolHandler(context.Background(), tool.Env{}, json.RawMessage(`{"title":"Oppenheimer","type":"movie","year":2023}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "No movie or series found") {
		t.Errorf("out = %q", out)
	}
	if len(paths) != 1 || !strings.HasPrefix(paths[0], "/search/movie?") || !strings.Contains(paths[0], "year=2023") {
		t.Errorf("paths = %v", paths)
	}
}
