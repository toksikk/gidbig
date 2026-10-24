package gidbig

import (
	"testing"

	"github.com/toksikk/gidbig/internal/bot"
	"github.com/toksikk/gidbig/internal/cfg"
	"github.com/toksikk/gidbig/internal/tool"
)

func TestRegisterTMDB_GatedOnToken(t *testing.T) {
	conf := &cfg.Config{}
	tools := tool.NewRegistry()
	b := bot.New(bot.Deps{Config: conf, Tools: tools})
	if registerTMDB(b, conf) {
		t.Fatal("registerTMDB without token = true")
	}
	if len(b.Router.Commands()) != 0 || len(tools.All()) != 0 {
		t.Fatal("disabled tmdb registered a command or tool")
	}

	conf.TMDB.ReadAccessToken = "tok"
	if !registerTMDB(b, conf) {
		t.Fatal("registerTMDB with token = false")
	}
	if cmds := b.Router.Commands(); len(cmds) != 1 || cmds[0].Name != "wheretostream" {
		t.Fatalf("commands = %+v", cmds)
	}
	if _, ok := tools.Get("streaming_providers"); !ok {
		t.Fatal("streaming_providers tool missing")
	}
}
