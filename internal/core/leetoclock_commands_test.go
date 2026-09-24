package gidbig

import (
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/leetoclock"
)

func TestAppendLeetoCommandsOnlyWhenReady(t *testing.T) {
	base := []*discordgo.ApplicationCommand{{Name: "status"}}
	module := leetoclock.New()
	if got := appendLeetoCommands(base, module, false); len(got) != 1 {
		t.Fatalf("failed init registered commands: %#v", got)
	}
	if got := appendLeetoCommands(base, module, true); len(got) != 2 || got[1].Name != "leetoclock" {
		t.Fatalf("ready module commands: %#v", got)
	}
}
