package configcmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/bot"
	"github.com/toksikk/gidbig/internal/cfg"
	"github.com/toksikk/gidbig/internal/llm"
	"github.com/toksikk/gidbig/internal/settings"
	"github.com/toksikk/gidbig/internal/shareddb"
)

const baseYAML = `
discord:
  token: "test-token"
gippity:
  allowed_guilds: ["guild"]
`

// newTestModule wires a module over a real (temporary) shared database and a
// real llm client, so the effective-value special cases for llm.model are
// covered rather than stubbed out.
func newTestModule(t *testing.T) (*Module, *settings.Store) {
	t.Helper()

	db, err := shareddb.Open(filepath.Join(t.TempDir(), shareddb.Filename))
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	base, err := cfg.Decode(strings.NewReader(baseYAML))
	if err != nil {
		t.Fatalf("decode base config: %v", err)
	}
	store, err := settings.New(db, base)
	if err != nil {
		t.Fatalf("settings.New: %v", err)
	}
	llmClient, err := llm.New(llm.Options{
		APIKey: "test-key",
		Model:  "boot-model",
		// Same wiring as the composition root, so the llm.model special case in
		// effective() is exercised against a real stored override.
		ModelOverride:       func() (string, bool) { return store.Override(settings.KeyLLMModel) },
		VisionModelOverride: func() (string, bool) { return store.Override(settings.KeyLLMVisionModel) },
	})
	if err != nil {
		t.Fatalf("llm.New: %v", err)
	}

	m := New()
	if err := m.Init(bot.Deps{Settings: store, LLM: llmClient}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return m, store
}

func TestAdminSubcommandGroup_Structure(t *testing.T) {
	group := New().AdminSubcommandGroup()
	if group.Name != groupName {
		t.Errorf("group name = %q, want %q", group.Name, groupName)
	}
	if group.Type != discordgo.ApplicationCommandOptionSubCommandGroup {
		t.Errorf("group type = %v, want SubCommandGroup", group.Type)
	}

	subs := make(map[string]*discordgo.ApplicationCommandOption)
	for _, o := range group.Options {
		subs[o.Name] = o
	}
	for _, want := range []string{"list", "get", "set", "unset"} {
		if subs[want] == nil {
			t.Errorf("missing subcommand %q", want)
		}
	}
	list := subs["list"]
	if list != nil && len(list.Options) != 0 {
		t.Errorf("list takes %d options, want none", len(list.Options))
	}
	for _, name := range []string{"get", "unset"} {
		if sub := subs[name]; sub != nil {
			if len(sub.Options) != 1 || sub.Options[0].Name != "key" || !sub.Options[0].Required {
				t.Errorf("%s must take one required key option, got %+v", name, sub.Options)
			}
		}
	}
	if set := subs["set"]; set != nil {
		if len(set.Options) != 2 || set.Options[0].Name != "key" || set.Options[1].Name != "value" {
			t.Fatalf("set options = %+v, want key and value", set.Options)
		}
		if !set.Options[1].Required {
			t.Error("set value option must be required")
		}
	}
}

func TestRenderList_ShowsValuesAndSourcesWithoutSecrets(t *testing.T) {
	m, _ := newTestModule(t)
	list := m.renderList()

	for _, want := range []string{"soundboard.queue_max_depth", "default"} {
		if !strings.Contains(list, want) {
			t.Errorf("list does not mention %q:\n%s", want, list)
		}
	}
	// llm.model has no config.yaml value, so the effective value comes from the
	// client's own default, not from an empty string.
	if !strings.Contains(list, "boot-model") {
		t.Errorf("list does not show the effective llm.model:\n%s", list)
	}
	for _, secret := range []string{"discord.token", "session_secret", "client_secret", "api_key"} {
		if strings.Contains(list, secret) {
			t.Errorf("list leaks %q:\n%s", secret, list)
		}
	}
}

func TestHandleAdminSubcommand_SetUnknownKeyIsIndistinguishableFromSecret(t *testing.T) {
	m, _ := newTestModule(t)
	unknown := handle(t, m, subcommand("set", keyValue("key", "nope"), keyValue("value", "1")))
	secret := handle(t, m, subcommand("set", keyValue("key", "discord.token"), keyValue("value", "x")))

	if !strings.Contains(unknown, "Unknown or non-editable") {
		t.Errorf("unknown key reply = %q", unknown)
	}
	if !strings.Contains(secret, "Unknown or non-editable") {
		t.Errorf("secret key reply = %q", secret)
	}
	if !strings.Contains(secret, "discord.token") {
		t.Errorf("reply should name the rejected input: %q", secret)
	}
}

func TestHandleAdminSubcommand_RejectionRepeatsCurrentValue(t *testing.T) {
	m, _ := newTestModule(t)
	reply := handle(t, m, subcommand("set",
		keyValue("key", settings.KeyGippityToolsReasoningEffort),
		keyValue("value", "bogus")))

	if !strings.Contains(reply, "Rejected") {
		t.Errorf("reply = %q, want a rejection", reply)
	}
	if !strings.Contains(reply, "unchanged") {
		t.Errorf("reply = %q, want the unchanged current value", reply)
	}
	if !strings.Contains(reply, "reasoning_effort") {
		t.Errorf("reply = %q, want the validation reason", reply)
	}
}

func TestHandleAdminSubcommand_SetGetUnsetRoundTrip(t *testing.T) {
	m, _ := newTestModule(t)

	set := handle(t, m, subcommand("set",
		keyValue("key", settings.KeyGippityToolsEnabled),
		keyValue("value", "true")))
	if !strings.Contains(set, "Set `gippity.tools.enabled=true`") || !strings.Contains(set, "source: `db`") {
		t.Errorf("set reply = %q", set)
	}

	get := handle(t, m, subcommand("get", keyValue("key", settings.KeyGippityToolsEnabled)))
	if !strings.Contains(get, "effective: `true`") || !strings.Contains(get, "source: `db`") {
		t.Errorf("get reply = %q", get)
	}

	unset := handle(t, m, subcommand("unset", keyValue("key", settings.KeyGippityToolsEnabled)))
	if !strings.Contains(unset, "Removed the runtime override") || !strings.Contains(unset, "source: `default`") {
		t.Errorf("unset reply = %q", unset)
	}

	again := handle(t, m, subcommand("unset", keyValue("key", settings.KeyGippityToolsEnabled)))
	if !strings.Contains(again, "No runtime override is set") {
		t.Errorf("second unset reply = %q", again)
	}
}

func TestHandleAdminSubcommand_SetLLMModelShowsTheRuntimeValue(t *testing.T) {
	m, _ := newTestModule(t)

	set := handle(t, m, subcommand("set",
		keyValue("key", settings.KeyLLMModel),
		keyValue("value", "runtime-model")))
	if !strings.Contains(set, "effective: `runtime-model`") || !strings.Contains(set, "source: `db`") {
		t.Errorf("set reply = %q", set)
	}
	get := handle(t, m, subcommand("get", keyValue("key", settings.KeyLLMModel)))
	if !strings.Contains(get, "effective: `runtime-model`") {
		t.Errorf("get reply = %q", get)
	}
	if list := m.renderList(); !strings.Contains(list, "runtime-model") {
		t.Errorf("list does not show the runtime model:\n%s", list)
	}
}

// TestHandleAdminSubcommand_EmptyVisionModelFollowsTextModel pins the documented
// acceptance of an empty value: it means "follow llm.model", unlike llm.model
// where empty is rejected.
func TestHandleAdminSubcommand_EmptyVisionModelFollowsTextModel(t *testing.T) {
	m, _ := newTestModule(t)
	reply := handle(t, m, subcommand("set",
		keyValue("key", settings.KeyLLMVisionModel),
		keyValue("value", "")))
	if !strings.Contains(reply, "effective: `boot-model`") {
		t.Errorf("reply = %q, want the text model as the effective vision model", reply)
	}
}

func TestHandleAdminSubcommand_RequiresAStore(t *testing.T) {
	m := New()
	if reply := m.renderList(); !strings.Contains(reply, "unavailable") {
		t.Errorf("renderList = %q, want an unavailable message", reply)
	}
	if reply := m.renderGet(settings.KeyGippityToolsEnabled); !strings.Contains(reply, "unavailable") {
		t.Errorf("renderGet = %q, want an unavailable message", reply)
	}
	if reply := handle(t, m, subcommand("list")); !strings.Contains(reply, "unavailable") {
		t.Errorf("list = %q, want an unavailable message", reply)
	}
	if reply := handle(t, m, subcommand("get", keyValue("key", settings.KeyGippityToolsEnabled))); !strings.Contains(reply, "unavailable") {
		t.Errorf("get = %q, want an unavailable message", reply)
	}
	if reply := handle(t, m, subcommand("set", keyValue("key", settings.KeyGippityToolsEnabled), keyValue("value", "true"))); !strings.Contains(reply, "unavailable") {
		t.Errorf("set = %q, want an unavailable message", reply)
	}
	if reply := handle(t, m, subcommand("unset", keyValue("key", settings.KeyGippityToolsEnabled))); !strings.Contains(reply, "unavailable") {
		t.Errorf("unset = %q, want an unavailable message", reply)
	}
}

func TestChunkContent_SplitsLongContent(t *testing.T) {
	if got := chunkContent("short", 100); len(got) != 1 || got[0] != "short" {
		t.Errorf("chunkContent(short) = %q", got)
	}
	if got := chunkContent("", 100); len(got) != 1 || got[0] != "" {
		t.Errorf("chunkContent(empty) = %q", got)
	}

	var lines []string
	for i := 0; i < 50; i++ {
		lines = append(lines, strings.Repeat("x", 100))
	}
	chunks := chunkContent(strings.Join(lines, "\n"), 300)
	if len(chunks) < 2 {
		t.Fatalf("got %d chunks, want several", len(chunks))
	}
	for _, c := range chunks {
		if len([]rune(c)) > 300 {
			t.Errorf("chunk has %d runes, want at most 300", len([]rune(c)))
		}
	}

	// A single long line is hard-split, and a multi-byte value is not cut in
	// half.
	hard := chunkContent(strings.Repeat("é", 10), 4)
	joined := strings.Join(hard, "")
	if joined != strings.Repeat("é", 10) {
		t.Errorf("hard split mangled the value: %q", joined)
	}
	for _, c := range hard {
		if len([]rune(c)) > 4 {
			t.Errorf("chunk %q exceeds the limit", c)
		}
	}
}

// --- helpers -------------------------------------------------------------

func keyValue(name, value string) *discordgo.ApplicationCommandInteractionDataOption {
	return &discordgo.ApplicationCommandInteractionDataOption{
		Name:  name,
		Type:  discordgo.ApplicationCommandOptionString,
		Value: value,
	}
}

func subcommand(name string, options ...*discordgo.ApplicationCommandInteractionDataOption) *discordgo.ApplicationCommandInteractionDataOption {
	return &discordgo.ApplicationCommandInteractionDataOption{
		Name:    name,
		Type:    discordgo.ApplicationCommandOptionSubCommand,
		Options: options,
	}
}

// handle runs one subcommand through the handler against a session whose REST
// calls go to a local test server, and returns the reply content.
func handle(t *testing.T, m *Module, sub *discordgo.ApplicationCommandInteractionDataOption) string {
	t.Helper()

	requests := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests <- body
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

	session, err := discordgo.New("Bot test")
	if err != nil {
		t.Fatalf("discordgo.New: %v", err)
	}
	m.HandleAdminSubcommand(session, adminInteraction(sub), sub)

	select {
	case body := <-requests:
		var payload struct {
			Content string `json:"content"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("decode reply %s: %v", body, err)
		}
		return payload.Content
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for a Discord request")
		return ""
	}
}

func adminInteraction(sub *discordgo.ApplicationCommandInteractionDataOption) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID: "interaction", AppID: "app", Token: "token",
			Type: discordgo.InteractionApplicationCommand,
			Data: discordgo.ApplicationCommandInteractionData{
				Name: "admin",
				Options: []*discordgo.ApplicationCommandInteractionDataOption{
					{
						Name:    groupName,
						Type:    discordgo.ApplicationCommandOptionSubCommandGroup,
						Options: []*discordgo.ApplicationCommandInteractionDataOption{sub},
					},
				},
			},
			Member: &discordgo.Member{User: &discordgo.User{ID: "owner-1"}},
		},
	}
}

var _ bot.Module = (*Module)(nil)
var _ bot.AdminProvider = (*Module)(nil)
