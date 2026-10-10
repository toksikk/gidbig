package gidbig

import (
	"bytes"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
)

func TestBanner_WritesToWriter(t *testing.T) {
	var buf bytes.Buffer
	Banner(&buf)

	output := buf.String()
	if len(output) == 0 {
		t.Error("Banner() wrote nothing to writer")
	}
}

func TestBanner_NilWriter(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Banner(nil) panicked: %v", r)
		}
	}()
	Banner(nil)
}

func TestStatusInteractionResponse_Owner(t *testing.T) {
	ownerID := "owner123"
	want := &discordgo.InteractionResponseData{Content: "some stats", Flags: discordgo.MessageFlagsEphemeral}

	resp := statusInteractionResponse(ownerID, ownerID, func() *discordgo.InteractionResponseData { return want })

	if resp == nil {
		t.Fatal("expected non-nil response")
		return
	}
	if resp.Type != discordgo.InteractionResponseChannelMessageWithSource {
		t.Errorf("Type = %v, want InteractionResponseChannelMessageWithSource", resp.Type)
	}
	if resp.Data != want {
		t.Errorf("Data = %+v, want built data", resp.Data)
	}
}

func TestBuildBotStatsMessageIncludesVersion(t *testing.T) {
	originalVersion := version
	version = "v1.2.3"
	t.Cleanup(func() { version = originalVersion })

	got := buildBotStatsMessage(&discordgo.Session{})
	if !strings.Contains(got, "gidbig v1.2.3") {
		t.Fatalf("status missing bot version:\n%s", got)
	}
}

func TestVersionFromBuildInfoIncludesRevisionForDevelopmentBuild(t *testing.T) {
	build := &debug.BuildInfo{
		Main: debug.Module{Version: "(devel)"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "0123456789abcdef0123456789abcdef01234567"},
			{Key: "vcs.modified", Value: "true"},
		},
	}
	if got := versionFromBuildInfo(build); got != "(devel 0123456789ab dirty)" {
		t.Fatalf("versionFromBuildInfo() = %q", got)
	}
}

func TestCurrentVersionUsesLinkedVersion(t *testing.T) {
	originalVersion := version
	version = "v1.2.3-4-g0123456"
	t.Cleanup(func() { version = originalVersion })

	if got := currentVersion(); got != "v1.2.3-4-g0123456" {
		t.Fatalf("currentVersion() = %q", got)
	}
}

func TestNormalizeVersionWrapsBareCommitHash(t *testing.T) {
	originalVersion := version
	defer func() { version = originalVersion }()

	version = "b21666f"
	if got := currentVersion(); got != "(devel b21666f)" {
		t.Fatalf("currentVersion() = %q, want \"(devel b21666f)\"", got)
	}
}

func TestNormalizeVersionKeepsTag(t *testing.T) {
	if got := normalizeVersion("v0.37.1"); got != "v0.37.1" {
		t.Fatalf("normalizeVersion() = %q, want \"v0.37.1\"", got)
	}
	if got := normalizeVersion("v0.37.1-1-gb21666f"); got != "v0.37.1-1-gb21666f" {
		t.Fatalf("normalizeVersion() = %q, want \"v0.37.1-1-gb21666f\"", got)
	}
	if got := normalizeVersion("(devel 0123456789ab dirty)"); got != "(devel 0123456789ab dirty)" {
		t.Fatalf("normalizeVersion() = %q, want original dev form", got)
	}
}

func TestStartedStatusUsesCurrentVersion(t *testing.T) {
	originalVersion, originalBuilddate := version, builddate
	version, builddate = "v1.2.3-4-g0123456", "2026-08-13T12:00:00Z"
	t.Cleanup(func() { version, builddate = originalVersion, originalBuilddate })

	if got := startedStatus(); got != "I just started! v1.2.3-4-g0123456 (2026-08-13T12:00:00Z)" {
		t.Fatalf("startedStatus() = %q", got)
	}
}

func TestStatusInteractionResponse_NonOwner(t *testing.T) {
	ownerID := "owner123"
	callerID := "rando456"

	called := false
	resp := statusInteractionResponse(callerID, ownerID, func() *discordgo.InteractionResponseData {
		called = true
		return &discordgo.InteractionResponseData{Content: "stats"}
	})

	if resp == nil {
		t.Fatal("expected non-nil response")
		return
	}
	if called {
		t.Error("buildStats should not be called for non-owner")
	}
	if resp.Data.Flags != discordgo.MessageFlagsEphemeral {
		t.Errorf("Flags = %v, want Ephemeral", resp.Data.Flags)
	}
	if resp.Data.Content != "Access denied." {
		t.Errorf("Content = %q, want %q", resp.Data.Content, "Access denied.")
	}
}
