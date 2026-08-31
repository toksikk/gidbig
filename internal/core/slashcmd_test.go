package gidbig

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/cfg"
)

type discordRequest struct {
	method string
	path   string
	body   []byte
}

func discordTestSession(t *testing.T) (*discordgo.Session, <-chan discordRequest) {
	t.Helper()
	requests := make(chan discordRequest, 10)
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

func coreInteraction(name, guildID, userID string, options ...*discordgo.ApplicationCommandInteractionDataOption) *discordgo.InteractionCreate {
	i := &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		ID: "interaction", AppID: "app", Token: "token", Type: discordgo.InteractionApplicationCommand,
		GuildID: guildID, Data: discordgo.ApplicationCommandInteractionData{Name: name, Options: options},
	}}
	if guildID == "" {
		i.User = &discordgo.User{ID: userID}
	} else {
		i.Member = &discordgo.Member{User: &discordgo.User{ID: userID}}
	}
	return i
}

func awaitDiscordRequest(t *testing.T, requests <-chan discordRequest) discordRequest {
	t.Helper()
	select {
	case req := <-requests:
		return req
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Discord request")
		return discordRequest{}
	}
}

func responseContent(t *testing.T, body []byte) string {
	t.Helper()
	var payload struct {
		Data    *discordgo.InteractionResponseData `json:"data"`
		Content *string                            `json:"content"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Data != nil {
		return payload.Data.Content
	}
	if payload.Content != nil {
		return *payload.Content
	}
	return ""
}

func TestCoreSlashCommands_PlayMetadata(t *testing.T) {
	commands := coreSlashCommands()
	if len(commands) != 3 || commands[0].Name != "list" || commands[1].Name != "uptime" || commands[2].Name != "play" {
		t.Fatalf("unexpected core commands: %#v", commands)
	}
	play := commands[2]
	if !strings.Contains(play.Description, "random") {
		t.Errorf("play description does not describe random playback: %q", play.Description)
	}
	collection := play.Options[0]
	if !collection.Required || collection.MaxLength != 100 || len(collection.Choices) != 0 {
		t.Errorf("collection option = %#v, want required free-form string with max length 100", collection)
	}
}

func TestBuildListMessage_BoundaryAndOversizedCollection(t *testing.T) {
	marker := "\n...\n(remaining collections omitted)"
	tests := []struct {
		name        string
		collections []*soundCollection
		truncated   bool
	}{
		{"exact limit", []*soundCollection{{Prefix: strings.Repeat("x", maxContentLen-6)}}, false},
		{"oversized prefix", []*soundCollection{{Prefix: strings.Repeat("x", maxContentLen*2)}}, true},
		{"oversized sound", []*soundCollection{{Prefix: "one", Sounds: []*soundClip{{Name: strings.Repeat("y", maxContentLen*2)}}}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			orig := COLLECTIONS
			COLLECTIONS = tt.collections
			t.Cleanup(func() { COLLECTIONS = orig })
			out := buildListMessage()
			if len(out) > maxContentLen {
				t.Fatalf("message is %d bytes, want <= %d", len(out), maxContentLen)
			}
			if strings.HasSuffix(out, marker) != tt.truncated {
				t.Errorf("truncation marker presence = %v, want %v", strings.HasSuffix(out, marker), tt.truncated)
			}
		})
	}
}

func TestCoreSlashHandlers_ListAndUptime(t *testing.T) {
	origConf, origCollections := conf, COLLECTIONS
	conf = &cfg.Config{}
	conf.Discord.OwnerID = "owner"
	COLLECTIONS = []*soundCollection{{Prefix: "memes", Sounds: []*soundClip{{Name: "wow"}}}}
	t.Cleanup(func() { conf, COLLECTIONS = origConf, origCollections })

	tests := []struct {
		name, command, user, contains string
	}{
		{"list", "list", "user", "**memes**"},
		{"uptime owner", "uptime", "owner", "Uptime:"},
		{"uptime denied", "uptime", "user", "Access denied."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, requests := discordTestSession(t)
			onCoreSlashInteractionCreate(s, coreInteraction(tt.command, "guild", tt.user))
			req := awaitDiscordRequest(t, requests)
			if !strings.Contains(responseContent(t, req.body), tt.contains) {
				t.Errorf("response %s does not contain %q", req.body, tt.contains)
			}
			var response discordgo.InteractionResponse
			if err := json.Unmarshal(req.body, &response); err != nil {
				t.Fatal(err)
			}
			if response.Data.Flags != discordgo.MessageFlagsEphemeral {
				t.Errorf("response flags = %v, want ephemeral", response.Data.Flags)
			}
		})
	}
}

func TestPlayHandler_DMDefersThenEditsError(t *testing.T) {
	s, requests := discordTestSession(t)
	onCoreSlashInteractionCreate(s, coreInteraction("play", "", "user"))
	deferred := awaitDiscordRequest(t, requests)
	var response discordgo.InteractionResponse
	if err := json.Unmarshal(deferred.body, &response); err != nil {
		t.Fatal(err)
	}
	if response.Type != discordgo.InteractionResponseDeferredChannelMessageWithSource || response.Data.Flags != discordgo.MessageFlagsEphemeral {
		t.Errorf("deferred response = %#v, want ephemeral defer", response)
	}
	edit := awaitDiscordRequest(t, requests)
	if got := responseContent(t, edit.body); !strings.Contains(got, "only be used in a server") {
		t.Errorf("DM terminal response = %q", got)
	}
}

func TestPlayHandler_ValidAndInvalidTerminalPaths(t *testing.T) {
	origCollections, origEnqueue := COLLECTIONS, playEnqueue
	COLLECTIONS = []*soundCollection{{Prefix: "MeMeS", Sounds: []*soundClip{{Name: "Wow", Weight: 1}}, soundRange: 1}}
	t.Cleanup(func() { COLLECTIONS, playEnqueue = origCollections, origEnqueue })

	tests := []struct {
		name, collection, sound, want string
		queued                        bool
	}{
		{"valid case insensitive", "memes", "wow", "Queued `Wow`", true},
		{"valid random", "MEMES", "", "Queued `Wow`", true},
		{"invalid collection", "missing", "wow", "Unknown collection", false},
		{"invalid sound", "memes", "missing", "was not found", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, requests := discordTestSession(t)
			if err := s.State.GuildAdd(&discordgo.Guild{ID: "guild", Name: "Test"}); err != nil {
				t.Fatal(err)
			}
			queued := make(chan struct{}, 1)
			playEnqueue = func(*discordgo.User, *discordgo.Guild, *soundCollection, *soundClip) { queued <- struct{}{} }
			i := coreInteraction("play", "guild", "user",
				&discordgo.ApplicationCommandInteractionDataOption{Name: "collection", Value: tt.collection},
				&discordgo.ApplicationCommandInteractionDataOption{Name: "sound", Value: tt.sound})
			onCoreSlashInteractionCreate(s, i)
			_ = awaitDiscordRequest(t, requests)
			edit := awaitDiscordRequest(t, requests)
			if got := responseContent(t, edit.body); !strings.Contains(got, tt.want) {
				t.Errorf("terminal response = %q, want containing %q", got, tt.want)
			}
			select {
			case <-queued:
				if !tt.queued {
					t.Error("invalid request was enqueued")
				}
			default:
				if tt.queued {
					t.Error("valid request was not enqueued")
				}
			}
		})
	}
}

func TestSoundCollection_Lookup_CaseInsensitive(t *testing.T) {
	sc := &soundCollection{Sounds: []*soundClip{{Name: "Airhorn"}}}
	if got := sc.Lookup("AIRHORN"); got == nil || got.Name != "Airhorn" {
		t.Errorf("Lookup(AIRHORN) = %#v", got)
	}
}
