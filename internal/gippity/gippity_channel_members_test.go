package gippity

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/tool"
)

func TestRegisterChannelMembersTool(t *testing.T) {
	m, _ := setupGippityTest(t)
	m.tools = tool.NewRegistry()

	m.registerChannelMembersTool()

	def, ok := m.tools.Get("get_channel_members")
	if !ok {
		t.Fatal("get_channel_members tool was not registered")
	}
	if def.Handler == nil {
		t.Fatal("tool registered without a handler")
	}
}

func TestChannelMembersToolOffered(t *testing.T) {
	m, _ := setupGippityTest(t)

	// Tools disabled: not offered.
	m.toolsEnabled = false
	m.tools = tool.NewRegistry()
	m.registerChannelMembersTool()
	if m.channelMembersToolOffered() {
		t.Error("tool offered while tools are disabled")
	}

	// Tools enabled with no allowlist: offered.
	m.toolsEnabled = true
	if !m.channelMembersToolOffered() {
		t.Error("tool not offered while tools are enabled")
	}

	// Allowlist excluding the tool: not offered.
	m.toolAllowed = map[string]bool{"search_chat_history": true}
	if m.channelMembersToolOffered() {
		t.Error("tool offered while excluded by the allowlist")
	}
}

func TestGetChannelMembersHandlerReturnsMembers(t *testing.T) {
	m, _ := setupGippityTest(t)
	var gotChannel string
	m.channelMembersFunc = func(_ *discordgo.Session, channelID string) (string, error) {
		gotChannel = channelID
		return "Alice, Bob", nil
	}

	got, err := m.getChannelMembersHandler(context.Background(), tool.Env{ChannelID: "chan-1"}, nil)
	if err != nil {
		t.Fatalf("getChannelMembersHandler: %v", err)
	}
	if gotChannel != "chan-1" {
		t.Errorf("looked up channel = %q, want %q", gotChannel, "chan-1")
	}
	if !strings.Contains(got, "Alice, Bob") {
		t.Errorf("result = %q, want it to contain the member list", got)
	}
}

func TestGetChannelMembersHandlerEmptyCases(t *testing.T) {
	m, _ := setupGippityTest(t)

	got, err := m.getChannelMembersHandler(context.Background(), tool.Env{}, nil)
	if err != nil {
		t.Fatalf("no channel: %v", err)
	}
	if got != "no channel available" {
		t.Errorf("no channel result = %q", got)
	}

	m.channelMembersFunc = func(_ *discordgo.Session, _ string) (string, error) { return "", nil }
	got, err = m.getChannelMembersHandler(context.Background(), tool.Env{ChannelID: "chan-1"}, nil)
	if err != nil {
		t.Fatalf("no members: %v", err)
	}
	if got != "no members found in this channel" {
		t.Errorf("no members result = %q", got)
	}
}

func TestGetChannelMembersHandlerSurfacesLookupError(t *testing.T) {
	m, _ := setupGippityTest(t)
	m.channelMembersFunc = func(_ *discordgo.Session, _ string) (string, error) {
		return "", errors.New("Missing Access")
	}

	got, err := m.getChannelMembersHandler(context.Background(), tool.Env{ChannelID: "chan-1"}, nil)
	if err != nil {
		t.Fatalf("handler should surface lookup errors as a result, got error: %v", err)
	}
	if !strings.Contains(got, "could not list channel members") || !strings.Contains(got, "Missing Access") {
		t.Errorf("result = %q, want it to name the lookup failure", got)
	}
}
