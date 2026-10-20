package configcmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/bot"
	"github.com/toksikk/gidbig/internal/cfg"
	"github.com/toksikk/gidbig/internal/llm"
	"github.com/toksikk/gidbig/internal/settings"
	"github.com/toksikk/gidbig/internal/shareddb"
	"gorm.io/gorm"
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
	m, store, _ := newTestModuleWithDB(t)
	return m, store
}

// newTestModuleWithDB also returns the database, for the tests that write a row
// outside the command the way a hand-edited sqlite3 session would.
func newTestModuleWithDB(t *testing.T) (*Module, *settings.Store, *gorm.DB) {
	t.Helper()
	return newTestModuleFull(t, baseYAML)
}

// newTestModuleWithYAML wires the module over a custom config.yaml.
func newTestModuleWithYAML(t *testing.T, yaml string) (*Module, *settings.Store) {
	t.Helper()
	m, store, _ := newTestModuleFull(t, yaml)
	return m, store
}

func newTestModuleFull(t *testing.T, yaml string) (*Module, *settings.Store, *gorm.DB) {
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

	base, err := cfg.Decode(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("decode base config: %v", err)
	}
	store, err := settings.New(db, base)
	if err != nil {
		t.Fatalf("settings.New: %v", err)
	}
	llmClient, err := llm.New(llm.Options{
		APIKey:    "test-key",
		Model:     "boot-model",
		MaxTokens: int64(cfgDefaultMaxTokens),
		// Same wiring as the composition root, so every llm.* special case in
		// effective() is exercised against a real stored override.
		ModelOverride:       func() (string, bool) { return store.Override(settings.KeyLLMModel) },
		VisionModelOverride: func() (string, bool) { return store.Override(settings.KeyLLMVisionModel) },
		MaxTokensOverride:   func() (int, bool) { return store.OverrideInt(settings.KeyLLMMaxTokens) },
	})
	if err != nil {
		t.Fatalf("llm.New: %v", err)
	}

	m := New()
	if err := m.Init(bot.Deps{Settings: store, LLM: llmClient}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return m, store, db
}

// cfgDefaultMaxTokens mirrors cfg's default for llm.max_tokens. The fixture
// builds its config by hand rather than through cfg.Decode, so it has to name
// the value the boot path would apply.
const cfgDefaultMaxTokens = 300

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
	if len(group.Options) != 6 {
		t.Errorf("group has %d subcommands, want 6", len(group.Options))
	}
	for _, name := range []string{"add", "remove"} {
		if sub := subs[name]; sub != nil {
			if len(sub.Options) != 2 || sub.Options[0].Name != "key" || sub.Options[1].Name != "value" ||
				!sub.Options[0].Required || !sub.Options[1].Required {
				t.Errorf("%s must take required key and value options, got %+v", name, sub.Options)
			}
		}
	}
	for _, want := range []string{"list", "get", "set", "unset", "add", "remove"} {
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

// TestHandleAdminSubcommand_IntKeyRoundTrip covers the integer key kind through
// the command, including the rejection of a non-positive value.
func TestHandleAdminSubcommand_IntKeyRoundTrip(t *testing.T) {
	m, _ := newTestModule(t)

	get := handle(t, m, subcommand("get", keyValue("key", settings.KeyLLMMaxTokens)))
	if !strings.Contains(get, "effective: `300`") || !strings.Contains(get, "source: `default`") {
		t.Errorf("get reply = %q, want the boot default", get)
	}

	set := handle(t, m, subcommand("set",
		keyValue("key", settings.KeyLLMMaxTokens),
		keyValue("value", "700")))
	if !strings.Contains(set, "effective: `700`") || !strings.Contains(set, "source: `db`") {
		t.Errorf("set reply = %q", set)
	}

	rejected := handle(t, m, subcommand("set",
		keyValue("key", settings.KeyLLMMaxTokens),
		keyValue("value", "0")))
	if !strings.Contains(rejected, "Rejected") || !strings.Contains(rejected, "unchanged: `700`") {
		t.Errorf("rejection reply = %q, want the unchanged 700", rejected)
	}

	unset := handle(t, m, subcommand("unset", keyValue("key", settings.KeyLLMMaxTokens)))
	if !strings.Contains(unset, "effective: `300`") {
		t.Errorf("unset reply = %q, want the boot default back", unset)
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

// TestEffective_MalformedMaxTokensRowReportsTheCapInForce covers a row written
// outside the command: the client floors an unparseable value to its default, so
// the command must not echo the raw row as if it were the cap being used.
func TestEffective_MalformedMaxTokensRowReportsTheCapInForce(t *testing.T) {
	_, _, db := newTestModuleWithDB(t)
	if err := db.Exec(
		"INSERT INTO settings (key, value, updated_by) VALUES (?, ?, ?)",
		settings.KeyLLMMaxTokens, "not-a-number", "manual",
	).Error; err != nil {
		t.Fatalf("insert row: %v", err)
	}

	// A fresh store over the same database, as a restart would build.
	base, err := cfg.Decode(strings.NewReader(baseYAML))
	if err != nil {
		t.Fatalf("decode base config: %v", err)
	}
	store, err := settings.New(db, base)
	if err != nil {
		t.Fatalf("settings.New: %v", err)
	}
	client, err := llm.New(llm.Options{
		APIKey:            "test-key",
		MaxTokens:         int64(cfgDefaultMaxTokens),
		MaxTokensOverride: func() (int, bool) { return store.OverrideInt(settings.KeyLLMMaxTokens) },
	})
	if err != nil {
		t.Fatalf("llm.New: %v", err)
	}
	m := New()
	if err := m.Init(bot.Deps{Settings: store, LLM: client}); err != nil {
		t.Fatalf("Init: %v", err)
	}

	value, _, ok := m.effective(settings.KeyLLMMaxTokens)
	if !ok {
		t.Fatal("llm.max_tokens is not a known key")
	}
	if value != strconv.Itoa(cfgDefaultMaxTokens) {
		t.Errorf("effective = %q, want the cap in force (%d)", value, cfgDefaultMaxTokens)
	}

	reply := handle(t, m, subcommand("get", keyValue("key", settings.KeyLLMMaxTokens)))
	if strings.Contains(reply, "not-a-number") {
		t.Errorf("reply echoes the malformed row: %q", reply)
	}
}

// TestHandleAdminSubcommand_EmptyClassifierModelShowsTheTextModel covers the one
// key whose empty value means "use another configured value": the classifier
// falls back to llm.model, so the command must not report an empty effective
// model that nothing uses.
func TestHandleAdminSubcommand_EmptyClassifierModelShowsTheTextModel(t *testing.T) {
	m, _ := newTestModule(t)

	get := handle(t, m, subcommand("get", keyValue("key", settings.KeyGippityClassifierModel)))
	if !strings.Contains(get, "effective: `boot-model`") {
		t.Errorf("get reply = %q, want the text model the classifier falls back to", get)
	}

	set := handle(t, m, subcommand("set",
		keyValue("key", settings.KeyGippityClassifierModel),
		keyValue("value", "vendor/classifier")))
	if !strings.Contains(set, "effective: `vendor/classifier`") {
		t.Errorf("set reply = %q, want the explicit model", set)
	}
}

const toolsOnYAML = `
discord:
  token: "test-token"
gippity:
  allowed_guilds: ["guild"]
  tools:
    enabled: true
    max_iterations: 3
`

// TestRenderList_WarnsWhenTheEffectiveConfigurationIsInvalid covers the state a
// config.yaml edit can create for a row written earlier: list must say so,
// because every write is rejected until it is fixed.
func TestRenderList_WarnsWhenTheEffectiveConfigurationIsInvalid(t *testing.T) {
	_, _, db := newTestModuleWithDB(t)
	if err := db.Exec(
		"INSERT INTO settings (key, value, updated_by) VALUES (?, ?, ?)",
		settings.KeyGippityToolsMaxIterations, "0", "owner",
	).Error; err != nil {
		t.Fatalf("insert row: %v", err)
	}

	// A deployment whose config.yaml now enables tools.
	base, err := cfg.Decode(strings.NewReader(toolsOnYAML))
	if err != nil {
		t.Fatalf("decode base config: %v", err)
	}
	store, err := settings.New(db, base)
	if err != nil {
		t.Fatalf("settings.New: %v", err)
	}
	client, err := llm.New(llm.Options{APIKey: "test-key", Model: "boot-model"})
	if err != nil {
		t.Fatalf("llm.New: %v", err)
	}
	m := New()
	if err := m.Init(bot.Deps{Settings: store, LLM: client}); err != nil {
		t.Fatalf("Init: %v", err)
	}

	list := m.renderList()
	if !strings.Contains(list, "INVALID CONFIGURATION") {
		t.Errorf("list does not warn about the invalid configuration:\n%s", list)
	}
	if !strings.Contains(list, "max_iterations") {
		t.Errorf("list does not name the offending key:\n%s", list)
	}
	// The values stay visible so the owner can fix them.
	if !strings.Contains(list, "gippity.tools.max_iterations") {
		t.Errorf("list must still show the values in force:\n%s", list)
	}
}

// TestEntries_CarryWhatTheListRenders keeps the web panel and the Discord reply
// on one definition: every key the table shows is one the list prints with the
// same value and source.
func TestEntries_CarryWhatTheListRenders(t *testing.T) {
	m, store := newTestModule(t)
	if err := store.Set(settings.KeyLLMModel, "runtime-model", "owner"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	entries, err := m.Entries()
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("Entries returned nothing")
	}

	list := m.renderList()
	for _, e := range entries {
		if !strings.Contains(list, e.Name) {
			t.Errorf("entry %q is not in the list reply", e.Name)
		}
		if !strings.Contains(list, e.Value) {
			t.Errorf("entry %q value %q is not in the list reply", e.Name, e.Value)
		}
		// The columns the list prints are the ones the panel renders.
		if !strings.Contains(list, fmt.Sprintf("%-42s %-8s %s", e.Name, e.Source, e.Value)) {
			t.Errorf("list does not render entry %q with source %q\n%s", e.Name, e.Source, list)
		}
	}

	for _, e := range entries {
		if e.Name == settings.KeyLLMModel {
			if e.Value != "runtime-model" || e.Source != settings.SourceDB {
				t.Errorf("llm.model entry = %+v, want the override and source db", e)
			}
			return
		}
	}
	t.Error("llm.model is missing from Entries")
}

// TestEntries_ReturnsTheEntriesWithAnInvalidCombination matches the list reply:
// the values in force are exactly what the operator needs to see to fix it.
func TestEntries_ReturnsTheEntriesWithAnInvalidCombination(t *testing.T) {
	_, _, db := newTestModuleWithDB(t)
	if err := db.Exec(
		"INSERT INTO settings (key, value, updated_by) VALUES (?, ?, ?)",
		settings.KeyGippityToolsMaxIterations, "0", "owner",
	).Error; err != nil {
		t.Fatalf("insert row: %v", err)
	}

	base, err := cfg.Decode(strings.NewReader(toolsOnYAML))
	if err != nil {
		t.Fatalf("decode base config: %v", err)
	}
	store, err := settings.New(db, base)
	if err != nil {
		t.Fatalf("settings.New: %v", err)
	}
	m := New()
	if err := m.Init(bot.Deps{Settings: store}); err != nil {
		t.Fatalf("Init: %v", err)
	}

	entries, err := m.Entries()
	if err == nil {
		t.Fatal("Entries did not report the invalid combination")
	}
	if len(entries) == 0 {
		t.Fatal("Entries returned no entries to repair the configuration with")
	}
}

func TestEntries_RequiresAStore(t *testing.T) {
	entries, err := New().Entries()
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
	if entries != nil {
		t.Errorf("entries = %v, want none", entries)
	}
}

// TestApplySet_ReportsWhetherItApplied is what the web panel marks the outcome
// with, so it must be false for every refused write.
func TestApplySet_ReportsWhetherItApplied(t *testing.T) {
	m, _ := newTestModule(t)

	message, ok := m.ApplySet(settings.KeyGippityToolsEnabled, "true", "owner")
	if !ok {
		t.Errorf("ok = false for a valid write: %q", message)
	}
	if !strings.Contains(message, "Set `gippity.tools.enabled=true`") {
		t.Errorf("message = %q", message)
	}

	message, ok = m.ApplySet(settings.KeyGippityToolsReasoningEffort, "bogus", "owner")
	if ok {
		t.Errorf("ok = true for a rejected value: %q", message)
	}
	if !strings.Contains(message, "Rejected") {
		t.Errorf("message = %q, want a rejection", message)
	}

	message, ok = m.ApplySet("nope", "1", "owner")
	if ok {
		t.Errorf("ok = true for an unknown key: %q", message)
	}
	if !strings.Contains(message, "Unknown or non-editable") {
		t.Errorf("message = %q", message)
	}

	message, ok = New().ApplySet(settings.KeyGippityToolsEnabled, "true", "owner")
	if ok || !strings.Contains(message, "unavailable") {
		t.Errorf("unwired module: ok = %v, message = %q", ok, message)
	}
}

// TestApplyUnset_ReportsWhetherItRemovedSomething distinguishes "removed" from
// "there was nothing to remove", which the panel shows as a failure.
func TestApplyUnset_ReportsWhetherItRemovedSomething(t *testing.T) {
	m, _ := newTestModule(t)

	message, ok := m.ApplyUnset(settings.KeyGippityToolsEnabled, "owner")
	if ok {
		t.Errorf("ok = true with no override stored: %q", message)
	}
	if !strings.Contains(message, "No runtime override is set") {
		t.Errorf("message = %q", message)
	}

	if _, ok := m.ApplySet(settings.KeyGippityToolsEnabled, "true", "owner"); !ok {
		t.Fatal("could not set the override for the unset test")
	}
	message, ok = m.ApplyUnset(settings.KeyGippityToolsEnabled, "owner")
	if !ok {
		t.Errorf("ok = false after removing an override: %q", message)
	}
	if !strings.Contains(message, "Removed the runtime override") {
		t.Errorf("message = %q", message)
	}

	message, ok = New().ApplyUnset(settings.KeyGippityToolsEnabled, "owner")
	if ok || !strings.Contains(message, "unavailable") {
		t.Errorf("unwired module: ok = %v, message = %q", ok, message)
	}
}

func TestColdKey_ReadOnlyEverywhere(t *testing.T) {
	m, _ := newTestModule(t)

	list := m.renderList()
	if !strings.Contains(list, "Read-only (restart required: edit config.yaml)") || !strings.Contains(list, "web.port") {
		t.Errorf("list does not mark cold keys:\n%s", list)
	}
	get := handle(t, m, subcommand("get", keyValue("key", "web.port")))
	if !strings.Contains(get, "restart required: edit config.yaml") || !strings.Contains(get, "source:") {
		t.Errorf("get(web.port) = %q", get)
	}
	unknown := handle(t, m, subcommand("set", keyValue("key", "nope"), keyValue("value", "1")))
	for name, sub := range map[string]*discordgo.ApplicationCommandInteractionDataOption{
		"set":    subcommand("set", keyValue("key", "web.port"), keyValue("value", "1")),
		"unset":  subcommand("unset", keyValue("key", "web.port")),
		"add":    subcommand("add", keyValue("key", "leetoclock.announcement_channels"), keyValue("value", "1")),
		"remove": subcommand("remove", keyValue("key", "leetoclock.announcement_channels"), keyValue("value", "1")),
	} {
		reply := handle(t, m, sub)
		if !strings.Contains(reply, "read-only at runtime") || reply == unknown {
			t.Errorf("%s on a cold key = %q, want a restart-required refusal", name, reply)
		}
	}
}

func TestAddRemove_Replies(t *testing.T) {
	m, store := newTestModule(t)
	key := settings.KeyGippityAllowedGuilds

	if reply := handle(t, m, subcommand("add", keyValue("key", key), keyValue("value", "g2"))); !strings.HasPrefix(reply, "Added `g2`") {
		t.Errorf("add reply = %q", reply)
	}
	if got := strings.Join(store.StringList(key), ","); got != "guild,g2" {
		t.Errorf("list = %q", got)
	}
	if reply := handle(t, m, subcommand("add", keyValue("key", key), keyValue("value", "g2"))); !strings.Contains(reply, "already contains") {
		t.Errorf("duplicate add reply = %q", reply)
	}
	if reply := handle(t, m, subcommand("remove", keyValue("key", key), keyValue("value", "nope"))); !strings.Contains(reply, "does not contain") {
		t.Errorf("missing remove reply = %q", reply)
	}
	if reply := handle(t, m, subcommand("remove", keyValue("key", key), keyValue("value", "guild"))); !strings.HasPrefix(reply, "Removed `guild`") {
		t.Errorf("remove reply = %q", reply)
	}
	if reply := handle(t, m, subcommand("remove", keyValue("key", key), keyValue("value", "g2"))); !strings.HasPrefix(reply, "Rejected: remove") {
		t.Errorf("removing the last guild reply = %q", reply)
	}
	if reply := handle(t, m, subcommand("add", keyValue("key", settings.KeyLLMMaxTokens), keyValue("value", "1"))); !strings.Contains(reply, "not a list setting") {
		t.Errorf("add on non-list reply = %q", reply)
	}
	if reply := handle(t, m, subcommand("add", keyValue("key", "discord.token"), keyValue("value", "1"))); !strings.Contains(reply, "Unknown or non-editable") {
		t.Errorf("add on secret reply = %q", reply)
	}
}

func TestEntries_ColdAndItems(t *testing.T) {
	m, _ := newTestModule(t)
	entries, err := m.Entries()
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	var sawCold, sawItems, sawDevMode bool
	for _, e := range entries {
		if e.Name == settings.KeyDevMode && !e.Cold && e.Note == settings.DevModeNote {
			sawDevMode = true
		}
		if e.Name == "web.port" && e.Cold && e.Note != "" {
			sawCold = true
		}
		if e.Name == settings.KeyGippityAllowedGuilds && !e.Cold && len(e.Items) == 1 && e.Items[0] == "guild" {
			sawItems = true
		}
	}
	if !sawCold || !sawItems || !sawDevMode {
		t.Errorf("entries missing cold, list items or hot dev_mode note: cold=%v items=%v dev_mode=%v", sawCold, sawItems, sawDevMode)
	}
}

func TestRedactURL(t *testing.T) {
	for in, want := range map[string]string{
		"":                                    "",
		"https://api.example.com/v1":          "https://api.example.com/v1",
		"https://user:sk-secret@host/v1":      "https://***@host/v1",
		"https://host/v1?api_key=sk-secret":   "https://host/v1?***",
		"https://u:sk-secret@host/p?key=sk-2": "https://***@host/p?***",
		"https://host/p#token=sk-secret":      "https://host/p#***",
		"not a url sk-secret":                 "***",
	} {
		if got := redactURL(in); got != want {
			t.Errorf("redactURL(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestBaseURLCredentialNeverShown covers every display surface: list, get, the
// cold-write refusal and Entries (the web panel).
func TestBaseURLCredentialNeverShown(t *testing.T) {
	m, _ := newTestModuleWithYAML(t, baseYAML+"llm:\n  base_url: \"https://user:sk-userinfo@llm.example/v1?api_key=sk-query\"\n")
	const want = "https://***@llm.example/v1?***"
	surfaces := map[string]string{
		"list":  m.renderList(),
		"get":   handle(t, m, subcommand("get", keyValue("key", settings.KeyLLMBaseURL))),
		"set":   handle(t, m, subcommand("set", keyValue("key", settings.KeyLLMBaseURL), keyValue("value", "x"))),
		"unset": handle(t, m, subcommand("unset", keyValue("key", settings.KeyLLMBaseURL))),
	}
	entries, err := m.Entries()
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	for _, e := range entries {
		if e.Name == settings.KeyLLMBaseURL {
			surfaces["entries"] = e.Value
		}
	}
	for name, out := range surfaces {
		if strings.Contains(out, "sk-userinfo") || strings.Contains(out, "sk-query") || strings.Contains(out, "user:") {
			t.Errorf("%s leaks the credential: %q", name, out)
		}
		if !strings.Contains(out, want) {
			t.Errorf("%s = %q, want it to contain %q", name, out, want)
		}
	}
}

// TestLongPersonality_IsTruncatedInRepliesButFullInEntries proves a long
// llm.personality does not flood list/get/set replies, while the web panel
// still gets the full text.
func TestLongPersonality_IsTruncatedInRepliesButFullInEntries(t *testing.T) {
	long := strings.Repeat("zqxw ", 1000) // 5000 runes
	m, _ := newTestModuleWithYAML(t, baseYAML+"llm:\n  personality: \""+long+"\"\n")
	want := "… (5000 chars)"
	surfaces := map[string]string{
		"list": m.renderList(),
		"get":  m.renderGet(settings.KeyLLMPersonality),
	}
	msg, ok := m.ApplySet(settings.KeyLLMPersonality, long+"!", "owner")
	if !ok {
		t.Fatalf("ApplySet failed: %s", msg)
	}
	surfaces["set"] = msg
	for name, out := range surfaces {
		if strings.Count(out, "zqxw") > 100 {
			t.Errorf("%s reply is not truncated (%d words)", name, strings.Count(out, "zqxw"))
		}
		if name != "set" && !strings.Contains(out, want) {
			t.Errorf("%s reply lacks %q", name, want)
		}
	}
	if !strings.Contains(surfaces["set"], "… (5001 chars)") {
		t.Errorf("set reply lacks the length marker: %s", surfaces["set"])
	}
	entries, _ := m.Entries()
	for _, e := range entries {
		if e.Name == settings.KeyLLMPersonality && e.Value != long+"!" {
			t.Errorf("Entries value has %d runes, want the full text", len([]rune(e.Value)))
		}
	}
}

func TestDisplayValue(t *testing.T) {
	if got := DisplayValue(settings.KindString, strings.Repeat("a", 500)); len(got) != 500 {
		t.Errorf("non-text value was changed")
	}
	if got := DisplayValue(settings.KindText, "a\nb"); got != "a b" {
		t.Errorf("DisplayValue(text, a\\nb) = %q", got)
	}
	got := DisplayValue(settings.KindText, strings.Repeat("é", 201))
	if got != strings.Repeat("é", 200)+"… (201 chars)" {
		t.Errorf("DisplayValue truncation = %q", got)
	}
}

func TestSetPreset_WarnsWhenCustomPersonalityIsInForce(t *testing.T) {
	m, _ := newTestModuleWithYAML(t, baseYAML+"llm:\n  personality: \"be a pirate\"\n")
	msg, ok := m.ApplySet(settings.KeyLLMPersonalityPreset, "dry", "owner")
	if !ok {
		t.Fatalf("ApplySet failed: %s", msg)
	}
	if !strings.Contains(msg, "warning: llm.personality (custom, source `yaml`) is in force") {
		t.Errorf("reply lacks the custom-wins warning: %s", msg)
	}

	m2, _ := newTestModule(t)
	msg, ok = m2.ApplySet(settings.KeyLLMPersonalityPreset, "dry", "owner")
	if !ok || strings.Contains(msg, "warning") {
		t.Errorf("reply = %q (ok=%v), want success without warning", msg, ok)
	}
}

func TestSetPreset_UnknownListsValidNames(t *testing.T) {
	m, _ := newTestModule(t)
	msg, ok := m.ApplySet(settings.KeyLLMPersonalityPreset, "nope", "owner")
	if ok {
		t.Fatalf("unknown preset accepted: %s", msg)
	}
	if !strings.Contains(msg, "dry, genalpha, hal, schemer") {
		t.Errorf("reply does not list the valid presets: %s", msg)
	}
}
