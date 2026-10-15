package admin

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/bot"
	"github.com/toksikk/gidbig/internal/cfg"
)

// stubCoffeeProvider is a minimal provider with a nested subcommand group.
func stubCoffeeProvider() *recordingProvider {
	return &recordingProvider{
		group: &discordgo.ApplicationCommandOption{
			Type:        discordgo.ApplicationCommandOptionSubCommandGroup,
			Name:        "coffee",
			Description: "Coffee admin queries",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "beverages",
					Description: "Show beverage settings for a user or all users",
				},
			},
		},
	}
}

// stubGippityProvider mirrors gippity.(*Module).AdminSubcommandGroup. Keep it in
// sync with internal/gippity/admin.go when the gippity admin schema changes.
func stubGippityProvider() *recordingProvider {
	userOpt := func(desc string) *discordgo.ApplicationCommandOption {
		return &discordgo.ApplicationCommandOption{
			Type:        discordgo.ApplicationCommandOptionUser,
			Name:        "user",
			Description: desc,
			Required:    false,
		}
	}
	return &recordingProvider{
		group: &discordgo.ApplicationCommandOption{
			Type:        discordgo.ApplicationCommandOptionSubCommandGroup,
			Name:        "gippity",
			Description: "Gippity admin queries",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "privacy",
					Description: "Show gippity privacy setting for a user or all users",
					Options:     []*discordgo.ApplicationCommandOption{userOpt("Target user (omit for all)")},
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "history",
					Description: "Show whether a user has stored conversation history",
					Options:     []*discordgo.ApplicationCommandOption{userOpt("Target user (omit for all)")},
				},
			},
		},
	}
}

// recordingProvider captures the subcommand handed to HandleAdminSubcommand.
type recordingProvider struct {
	group *discordgo.ApplicationCommandOption
	calls []*discordgo.ApplicationCommandInteractionDataOption
}

func (r *recordingProvider) AdminSubcommandGroup() *discordgo.ApplicationCommandOption {
	return r.group
}

func (r *recordingProvider) HandleAdminSubcommand(_ *discordgo.Session, _ *discordgo.InteractionCreate, sub *discordgo.ApplicationCommandInteractionDataOption) {
	r.calls = append(r.calls, sub)
}

var _ bot.AdminProvider = (*recordingProvider)(nil)

func newTestModule(providers ...bot.AdminProvider) *Module {
	m := New(providers...)
	_ = m.Init(bot.Deps{OwnerID: "owner-1"})
	return m
}

func TestModule_LifecycleNoOps(t *testing.T) {
	m := newTestModule(stubCoffeeProvider())
	if m.Name() != "admin" {
		t.Errorf("Name = %q, want %q", m.Name(), "admin")
	}
	if m.Background() != nil {
		t.Error("Background() should be nil")
	}
	if err := m.Shutdown(); err != nil {
		t.Errorf("Shutdown() = %v, want nil", err)
	}
}

func TestInit_OwnerIDFromConfig(t *testing.T) {
	m := New()
	conf := &cfg.Config{}
	conf.Discord.OwnerID = "cfg-owner"
	if err := m.Init(bot.Deps{Config: conf}); err != nil {
		t.Fatalf("Init() = %v", err)
	}
	if m.ownerID != "cfg-owner" {
		t.Errorf("ownerID = %q, want %q", m.ownerID, "cfg-owner")
	}
}

func TestCommands_Structure(t *testing.T) {
	cmd := newTestModule(stubCoffeeProvider(), stubGippityProvider(), stubConfigProvider()).command()
	if cmd.Name != "admin" {
		t.Errorf("Name = %q, want %q", cmd.Name, "admin")
	}

	names := make(map[string]bool)
	for _, opt := range cmd.Options {
		names[opt.Name] = true
	}
	for _, want := range []string{"coffee", "gippity", "config", "info"} {
		if !names[want] {
			t.Errorf("missing top-level option %q", want)
		}
	}
}

func TestCommands_NoProviders(t *testing.T) {
	cmd := newTestModule().command()
	if len(cmd.Options) != 1 || cmd.Options[0].Name != "info" {
		t.Errorf("expected only info subcommand, got %+v", cmd.Options)
	}
}

func TestCommands_CoffeeBeveragesSubcommand(t *testing.T) {
	cmd := newTestModule(stubCoffeeProvider()).command()
	coffeeGroup := findOption(cmd.Options, "coffee")
	if coffeeGroup == nil {
		t.Fatal("no coffee subcommand group")
		return
	}
	if coffeeGroup.Type != discordgo.ApplicationCommandOptionSubCommandGroup {
		t.Errorf("coffee Type = %v, want SubCommandGroup", coffeeGroup.Type)
	}
	beverages := findOption(coffeeGroup.Options, "beverages")
	if beverages == nil {
		t.Fatal("no beverages subcommand under coffee")
		return
	}
	if beverages.Type != discordgo.ApplicationCommandOptionSubCommand {
		t.Errorf("beverages Type = %v, want SubCommand", beverages.Type)
	}
}

func TestCommands_GippitySubcommands(t *testing.T) {
	cmd := newTestModule(stubGippityProvider()).command()
	gippityGroup := findOption(cmd.Options, "gippity")
	if gippityGroup == nil {
		t.Fatal("no gippity subcommand group")
		return
	}

	subNames := make(map[string]bool)
	for _, o := range gippityGroup.Options {
		subNames[o.Name] = true
	}
	for _, want := range []string{"privacy", "history"} {
		if !subNames[want] {
			t.Errorf("missing gippity subcommand %q", want)
		}
	}
}

func TestCommands_InfoSubcommand(t *testing.T) {
	cmd := newTestModule().command()
	info := findOption(cmd.Options, "info")
	if info == nil {
		t.Fatal("no info subcommand")
		return
	}
	if info.Type != discordgo.ApplicationCommandOptionSubCommand {
		t.Errorf("info Type = %v, want SubCommand", info.Type)
	}
}

func TestDispatch_RoutesToMatchingProvider(t *testing.T) {
	coffee := stubCoffeeProvider()
	gippity := stubGippityProvider()
	m := newTestModule(coffee, gippity)
	s, requests := adminDiscordSession(t)

	sub := &discordgo.ApplicationCommandInteractionDataOption{Name: "beverages", Type: discordgo.ApplicationCommandOptionSubCommand}
	m.onAdminInteractionCreate(s, adminInteraction("admin", ownerInteractionGroup("coffee", sub)))

	awaitRequest(t, requests) // deferred ephemeral
	if len(coffee.calls) != 1 {
		t.Fatalf("coffee provider calls = %d, want 1", len(coffee.calls))
	}
	if coffee.calls[0].Name != "beverages" {
		t.Errorf("coffee sub = %q, want %q", coffee.calls[0].Name, "beverages")
	}
	if len(gippity.calls) != 0 {
		t.Errorf("gippity provider calls = %d, want 0", len(gippity.calls))
	}
}

func TestDispatch_NonOwnerDenied(t *testing.T) {
	coffee := stubCoffeeProvider()
	m := newTestModule(coffee)
	s, requests := adminDiscordSession(t)

	sub := &discordgo.ApplicationCommandInteractionDataOption{Name: "beverages", Type: discordgo.ApplicationCommandOptionSubCommand}
	// The owner gate runs through the OwnerOnly middleware the /admin command is
	// registered with, so exercise it here.
	handler := bot.OwnerOnly("owner-1")(m.onAdminInteractionCreate)
	handler(s, adminInteraction("admin", ownerInteractionGroup("coffee", sub), "not-the-owner"))

	req := awaitRequest(t, requests)
	if req.method != http.MethodPost {
		t.Errorf("method = %s, want POST", req.method)
	}
	if len(coffee.calls) != 0 {
		t.Errorf("provider called for non-owner: %d calls", len(coffee.calls))
	}
	var resp discordgo.InteractionResponse
	if err := json.Unmarshal(req.body, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Type != discordgo.InteractionResponseChannelMessageWithSource {
		t.Errorf("response Type = %v, want ChannelMessageWithSource", resp.Type)
	}
	if resp.Data == nil || resp.Data.Content != "Access denied." {
		t.Errorf("response content = %+v, want Access denied.", resp.Data)
	}
	if resp.Data == nil || resp.Data.Flags != discordgo.MessageFlagsEphemeral {
		t.Errorf("response flags = %v, want ephemeral", resp.Data)
	}
}

func TestDispatch_InfoUsesInjectedRenderer(t *testing.T) {
	m := newTestModule()
	m.SetInfoText(func(*discordgo.Session) string { return "stats-here" })
	s, requests := adminDiscordSession(t)

	m.onAdminInteractionCreate(s, adminInteraction("admin", ownerInteractionGroup("info", nil)))

	awaitRequest(t, requests) // deferred ephemeral
	editReq := awaitRequest(t, requests)
	if !strings.Contains(string(editReq.body), "stats-here") {
		t.Errorf("edit body = %s, want it to contain rendered info", editReq.body)
	}
}

func TestDispatch_InfoWithoutRenderer(t *testing.T) {
	m := newTestModule()
	s, requests := adminDiscordSession(t)

	m.onAdminInteractionCreate(s, adminInteraction("admin", ownerInteractionGroup("info", nil)))

	awaitRequest(t, requests) // deferred ephemeral
	editReq := awaitRequest(t, requests)
	if !strings.Contains(string(editReq.body), "Info unavailable.") {
		t.Errorf("edit body = %s, want the missing-renderer fallback", editReq.body)
	}
}

func TestListener_WithoutInitDoesNotPanic(t *testing.T) {
	m := New(stubCoffeeProvider()) // deliberately no Init
	s, requests := adminDiscordSession(t)

	// No owner is configured before Init, so an empty-owner gate denies.
	handler := bot.OwnerOnly("")(m.onAdminInteractionCreate)
	handler(s, adminInteraction("admin", ownerInteractionGroup("info", nil)))

	req := awaitRequest(t, requests)
	if !strings.Contains(string(req.body), "Access denied.") {
		t.Errorf("body = %s, want the denial from the owner gate", req.body)
	}
}

func TestDispatch_UnknownGroupIsIgnored(t *testing.T) {
	m := newTestModule(stubCoffeeProvider())
	s, requests := adminDiscordSession(t)

	m.onAdminInteractionCreate(s, adminInteraction("admin", ownerInteractionGroup("nope", nil)))

	awaitRequest(t, requests) // deferred ephemeral, then no further request
	select {
	case req := <-requests:
		t.Fatalf("unexpected extra request: %+v", req)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestListener_IgnoresForeignCommand(t *testing.T) {
	m := newTestModule(stubCoffeeProvider())
	s, requests := adminDiscordSession(t)

	m.onAdminInteractionCreate(s, adminInteraction("gippity", nil))

	select {
	case req := <-requests:
		t.Fatalf("unexpected request for foreign command: %+v", req)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestListener_IgnoresNonCommandInteraction(t *testing.T) {
	m := newTestModule(stubCoffeeProvider())
	s, requests := adminDiscordSession(t)

	m.onAdminInteractionCreate(s, &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{Type: discordgo.InteractionMessageComponent, Data: discordgo.MessageComponentInteractionData{CustomID: "x"}},
	})

	select {
	case req := <-requests:
		t.Fatalf("unexpected request for component interaction: %+v", req)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestHandleAdminCommand_NoOptions(t *testing.T) {
	m := newTestModule(stubCoffeeProvider())
	s, requests := adminDiscordSession(t)

	m.onAdminInteractionCreate(s, adminInteraction("admin", nil))

	select {
	case req := <-requests:
		t.Fatalf("unexpected request for empty /admin: %+v", req)
	case <-time.After(50 * time.Millisecond):
	}
}

func findOption(opts []*discordgo.ApplicationCommandOption, name string) *discordgo.ApplicationCommandOption {
	for _, o := range opts {
		if o.Name == name {
			return o
		}
	}
	return nil
}

func ownerInteractionGroup(name string, sub *discordgo.ApplicationCommandInteractionDataOption) []*discordgo.ApplicationCommandInteractionDataOption {
	group := &discordgo.ApplicationCommandInteractionDataOption{
		Name: name,
		Type: discordgo.ApplicationCommandOptionSubCommandGroup,
	}
	if sub != nil {
		group.Options = []*discordgo.ApplicationCommandInteractionDataOption{sub}
	}
	return []*discordgo.ApplicationCommandInteractionDataOption{group}
}

// adminInteraction builds an /admin interaction from the given user.
func adminInteraction(name string, options []*discordgo.ApplicationCommandInteractionDataOption, userID ...string) *discordgo.InteractionCreate {
	uid := "owner-1"
	if len(userID) > 0 {
		uid = userID[0]
	}
	return &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID: "interaction", AppID: "app", Token: "token",
			Type: discordgo.InteractionApplicationCommand,
			Data: discordgo.ApplicationCommandInteractionData{Name: name, Options: options},
			Member: &discordgo.Member{
				User: &discordgo.User{ID: uid},
			},
		},
	}
}

type discordRequest struct {
	method string
	path   string
	body   []byte
}

// adminDiscordSession returns a session that routes all REST calls to a local
// test server, plus a channel of the captured requests. It swaps the
// process-global discordgo endpoint variables, so tests in this package must
// not call t.Parallel(); cleanup restores the originals after each test.
func adminDiscordSession(t *testing.T) (*discordgo.Session, <-chan discordRequest) {
	t.Helper()
	requests := make(chan discordRequest, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests <- discordRequest{method: r.Method, path: r.URL.Path, body: body}
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

func awaitRequest(t *testing.T, requests <-chan discordRequest) discordRequest {
	t.Helper()
	select {
	case req := <-requests:
		return req
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for a Discord request")
		return discordRequest{}
	}
}

// stubConfigProvider mirrors configcmd.(*Module).AdminSubcommandGroup. Keep it
// in sync with internal/configcmd when the config schema changes.
func stubConfigProvider() *recordingProvider {
	keyOption := func(description string) *discordgo.ApplicationCommandOption {
		return &discordgo.ApplicationCommandOption{
			Type:        discordgo.ApplicationCommandOptionString,
			Name:        "key",
			Description: description,
			Required:    true,
		}
	}
	return &recordingProvider{
		group: &discordgo.ApplicationCommandOption{
			Type:        discordgo.ApplicationCommandOptionSubCommandGroup,
			Name:        "config",
			Description: "Runtime settings (hot keys apply without a restart)",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "list",
					Description: "List every runtime-editable setting with its effective value and source",
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "get",
					Description: "Show one setting, its source and when it applies",
					Options:     []*discordgo.ApplicationCommandOption{keyOption("Setting key, for example gippity.tools.enabled")},
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "set",
					Description: "Set a runtime override; an invalid value is rejected",
					Options: []*discordgo.ApplicationCommandOption{
						keyOption("Setting key, for example gippity.tools.enabled"),
						{
							Type:        discordgo.ApplicationCommandOptionString,
							Name:        "value",
							Description: "New value; lists are comma-separated",
							Required:    true,
						},
					},
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "unset",
					Description: "Remove the runtime override, falling back to config.yaml",
					Options:     []*discordgo.ApplicationCommandOption{keyOption("Setting key to reset")},
				},
			},
		},
	}
}

func TestCommands_ConfigSubcommands(t *testing.T) {
	cmd := newTestModule(stubConfigProvider()).command()
	group := findOption(cmd.Options, "config")
	if group == nil {
		t.Fatal("no config subcommand group")
		return
	}
	if group.Type != discordgo.ApplicationCommandOptionSubCommandGroup {
		t.Errorf("config Type = %v, want SubCommandGroup", group.Type)
	}

	subNames := make(map[string]bool)
	for _, o := range group.Options {
		subNames[o.Name] = true
	}
	for _, want := range []string{"list", "get", "set", "unset"} {
		if !subNames[want] {
			t.Errorf("missing config subcommand %q", want)
		}
	}
}
