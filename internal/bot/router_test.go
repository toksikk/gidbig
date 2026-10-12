package bot

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
)

func makeRouter() *Router {
	return newRouter(Deps{})
}

func slashCmd(name string) *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{Name: name}
}

func slashInteraction(name string) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			Type: discordgo.InteractionApplicationCommand,
			Data: discordgo.ApplicationCommandInteractionData{Name: name},
		},
	}
}

func autocompleteInteraction(name string) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			Type: discordgo.InteractionApplicationCommandAutocomplete,
			Data: discordgo.ApplicationCommandInteractionData{Name: name},
		},
	}
}

func componentInteraction(customID string) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			Type: discordgo.InteractionMessageComponent,
			Data: discordgo.MessageComponentInteractionData{CustomID: customID},
		},
	}
}

func TestRouter_DispatchesKnownCommand(t *testing.T) {
	r := makeRouter()
	var called atomic.Bool
	r.AddCommand(slashCmd("ping"), func(_ *discordgo.Session, _ *discordgo.InteractionCreate) {
		called.Store(true)
	})

	r.onInteractionCreate(nil, slashInteraction("ping"))
	if !called.Load() {
		t.Fatal("handler not called for registered command")
	}
}

func TestRouter_IgnoresUnknownCommand(t *testing.T) {
	r := makeRouter()
	var called atomic.Bool
	r.AddCommand(slashCmd("ping"), func(_ *discordgo.Session, _ *discordgo.InteractionCreate) {
		called.Store(true)
	})

	r.onInteractionCreate(nil, slashInteraction("unknown"))
	if called.Load() {
		t.Fatal("handler called for unknown command")
	}
}

func TestRouter_DispatchesAutocompleteToCommand(t *testing.T) {
	r := makeRouter()
	var called atomic.Bool
	r.AddCommand(slashCmd("anti"), func(_ *discordgo.Session, i *discordgo.InteractionCreate) {
		if i.Type != discordgo.InteractionApplicationCommandAutocomplete {
			t.Errorf("Type = %v, want autocomplete", i.Type)
		}
		called.Store(true)
	})

	r.onInteractionCreate(nil, autocompleteInteraction("anti"))
	if !called.Load() {
		t.Fatal("autocomplete handler not called for registered command")
	}
}

func TestRouter_DispatchesComponentByPrefix(t *testing.T) {
	r := makeRouter()
	var called atomic.Bool
	r.AddComponent("coffee_", func(_ *discordgo.Session, _ *discordgo.InteractionCreate) {
		called.Store(true)
	})

	r.onInteractionCreate(nil, componentInteraction("coffee_brew"))
	if !called.Load() {
		t.Fatal("component handler not called for matching prefix")
	}
}

func TestRouter_IgnoresComponentWithNonMatchingPrefix(t *testing.T) {
	r := makeRouter()
	var called atomic.Bool
	r.AddComponent("coffee_", func(_ *discordgo.Session, _ *discordgo.InteractionCreate) {
		called.Store(true)
	})

	r.onInteractionCreate(nil, componentInteraction("other_action"))
	if called.Load() {
		t.Fatal("component handler called for non-matching prefix")
	}
}

func TestRouter_MiddlewareApplied(t *testing.T) {
	captureRespond(t)
	r := makeRouter()
	var called atomic.Bool
	r.AddCommand(slashCmd("ping"),
		func(_ *discordgo.Session, _ *discordgo.InteractionCreate) { called.Store(true) },
		OwnerOnly("owner"),
	)

	r.onInteractionCreate(nil, &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			Type:   discordgo.InteractionApplicationCommand,
			Member: &discordgo.Member{User: &discordgo.User{ID: "other"}},
			Data:   discordgo.ApplicationCommandInteractionData{Name: "ping"},
		},
	})
	if called.Load() {
		t.Fatal("handler called despite OwnerOnly middleware blocking non-owner")
	}
}

func TestRouter_RecoversCommandPanic(t *testing.T) {
	r := makeRouter()
	r.AddCommand(slashCmd("boom"), func(_ *discordgo.Session, _ *discordgo.InteractionCreate) {
		panic("boom")
	})

	defer func() {
		if rec := recover(); rec != nil {
			t.Fatalf("command panic escaped the router: %v", rec)
		}
	}()
	r.onInteractionCreate(nil, slashInteraction("boom"))
}

func TestRouter_DispatchesMessageListeners(t *testing.T) {
	r := makeRouter()
	called := make(chan struct{}, 1)
	r.OnMessage(func(_ *discordgo.Session, _ *discordgo.MessageCreate) {
		called <- struct{}{}
	})

	r.onMessageCreate(nil, &discordgo.MessageCreate{})
	select {
	case <-called:
	case <-time.After(2 * time.Second):
		t.Fatal("message listener not called")
	}
}

func TestRouter_NilCommandHandlerDoesNotPanic(t *testing.T) {
	r := makeRouter()
	r.AddCommand(slashCmd("ping"), nil)

	r.onInteractionCreate(nil, slashInteraction("ping"))
}

func TestRouter_CommandsInRegistrationOrder(t *testing.T) {
	r := makeRouter()
	r.AddCommand(slashCmd("ping"), func(_ *discordgo.Session, _ *discordgo.InteractionCreate) {})
	r.AddCommand(slashCmd("pong"), func(_ *discordgo.Session, _ *discordgo.InteractionCreate) {})

	cmds := r.Commands()
	if len(cmds) != 2 || cmds[0].Name != "ping" || cmds[1].Name != "pong" {
		t.Fatalf("Commands() = %#v, want ping then pong", cmds)
	}
}

func TestRouter_NilReceiverIsNoOp(t *testing.T) {
	var r *Router
	// A module Init that runs without a Router must not panic.
	r.AddCommand(slashCmd("ping"), func(_ *discordgo.Session, _ *discordgo.InteractionCreate) {})
	r.AddComponent("x", func(_ *discordgo.Session, _ *discordgo.InteractionCreate) {})
	r.OnMessage(func(_ *discordgo.Session, _ *discordgo.MessageCreate) {})
	r.OnReady(func(_ *discordgo.Session, _ *discordgo.Ready) {})
	if r.Commands() != nil {
		t.Error("Commands() on a nil Router should be nil")
	}
}

func TestRouter_DuplicateCommandIgnored(t *testing.T) {
	r := makeRouter()
	r.AddCommand(slashCmd("dup"), func(_ *discordgo.Session, _ *discordgo.InteractionCreate) {})
	r.AddCommand(slashCmd("dup"), func(_ *discordgo.Session, _ *discordgo.InteractionCreate) {})

	if got := len(r.Commands()); got != 1 {
		t.Fatalf("Commands() = %d, want 1 (duplicate rejected)", got)
	}
	if err := r.takeRegistrationError(); err == nil {
		t.Fatal("duplicate registration should record an error for RegisterModule")
	}
}

func TestRouter_ComponentLongestPrefixWins(t *testing.T) {
	r := makeRouter()
	var short, long atomic.Bool
	r.AddComponent("coffee_", func(_ *discordgo.Session, _ *discordgo.InteractionCreate) { short.Store(true) })
	r.AddComponent("coffee_brew_cfg", func(_ *discordgo.Session, _ *discordgo.InteractionCreate) { long.Store(true) })

	r.onInteractionCreate(nil, componentInteraction("coffee_brew_cfg:pick"))
	if !long.Load() {
		t.Fatal("longest matching prefix handler was not called")
	}
	if short.Load() {
		t.Fatal("shorter overlapping prefix handler was called")
	}
}

func TestRouter_RecoversComponentPanic(t *testing.T) {
	r := makeRouter()
	r.AddComponent("boom_", func(_ *discordgo.Session, _ *discordgo.InteractionCreate) {
		panic("component panic")
	})

	defer func() {
		if rec := recover(); rec != nil {
			t.Fatalf("component panic escaped the router: %v", rec)
		}
	}()
	r.onInteractionCreate(nil, componentInteraction("boom_x"))
}

func TestRouter_RecoversMessagePanic(t *testing.T) {
	r := makeRouter()
	done := make(chan struct{})
	r.OnMessage(func(_ *discordgo.Session, _ *discordgo.MessageCreate) {
		defer close(done)
		panic("message panic")
	})

	r.onMessageCreate(nil, &discordgo.MessageCreate{})
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("message handler did not run")
	}
}

// TestRouter_ConcurrentRegistrationAndDispatch registers commands while the
// dispatcher reads the tables, the window that exists while the gateway is
// already delivering events. Run with -race it proves the mutex coverage.
func TestRouter_ConcurrentRegistrationAndDispatch(t *testing.T) {
	r := makeRouter()
	var wg sync.WaitGroup

	for i := range 50 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r.AddCommand(&discordgo.ApplicationCommand{Name: fmt.Sprintf("cmd-%d", i)}, func(_ *discordgo.Session, _ *discordgo.InteractionCreate) {})
			r.AddComponent(fmt.Sprintf("c-%d", i), func(_ *discordgo.Session, _ *discordgo.InteractionCreate) {})
			r.OnMessage(func(_ *discordgo.Session, _ *discordgo.MessageCreate) {})
		}(i)

		wg.Add(1)
		go func() {
			defer wg.Done()
			r.onInteractionCreate(nil, slashInteraction("cmd-0"))
			r.onMessageCreate(nil, &discordgo.MessageCreate{})
		}()
	}
	wg.Wait()
}

func TestRouter_MultipleCommands(t *testing.T) {
	r := makeRouter()
	var pingCalled, pongCalled atomic.Bool
	r.AddCommand(slashCmd("ping"), func(_ *discordgo.Session, _ *discordgo.InteractionCreate) { pingCalled.Store(true) })
	r.AddCommand(slashCmd("pong"), func(_ *discordgo.Session, _ *discordgo.InteractionCreate) { pongCalled.Store(true) })

	r.onInteractionCreate(nil, slashInteraction("ping"))
	r.onInteractionCreate(nil, slashInteraction("pong"))

	if !pingCalled.Load() {
		t.Fatal("ping handler not called")
	}
	if !pongCalled.Load() {
		t.Fatal("pong handler not called")
	}
}
