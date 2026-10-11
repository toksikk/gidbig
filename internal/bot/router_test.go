package bot

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
)

func makeRouter() *Router {
	return newRouter(Deps{})
}

func slashInteraction(name string) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			Type: discordgo.InteractionApplicationCommand,
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
	r.AddCommand("ping", func(_ *discordgo.Session, _ *discordgo.InteractionCreate) {
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
	r.AddCommand("ping", func(_ *discordgo.Session, _ *discordgo.InteractionCreate) {
		called.Store(true)
	})

	r.onInteractionCreate(nil, slashInteraction("unknown"))
	if called.Load() {
		t.Fatal("handler called for unknown command")
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
	r.AddCommand("ping",
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

func TestRouter_DispatchesMessageListeners(t *testing.T) {
	r := makeRouter()
	called := make(chan struct{}, 1)
	r.messageHandlers = append(r.messageHandlers, func(_ *discordgo.Session, _ *discordgo.MessageCreate) {
		called <- struct{}{}
	})

	r.onMessageCreate(nil, &discordgo.MessageCreate{})
	select {
	case <-called:
	case <-time.After(2 * time.Second):
		t.Fatal("message listener not called")
	}
}

func TestRouter_DispatchesInteractionListeners(t *testing.T) {
	r := makeRouter()
	called := make(chan struct{}, 1)
	r.interactionHandlers = append(r.interactionHandlers, func(_ *discordgo.Session, _ *discordgo.InteractionCreate) {
		called <- struct{}{}
	})

	r.onInteractionCreate(nil, slashInteraction("anything"))
	select {
	case <-called:
	case <-time.After(2 * time.Second):
		t.Fatal("interaction listener not called")
	}
}

func TestRouter_NilCommandHandlerDoesNotPanic(t *testing.T) {
	r := makeRouter()
	r.AddCommand("ping", nil)

	r.onInteractionCreate(nil, slashInteraction("ping"))
}

func TestRouter_RegisterSortsListenersIntoBuckets(t *testing.T) {
	session, _ := discordgo.New("")
	r := newRouter(Deps{Session: session})
	mod := &listenerModule{}
	if err := r.register(mod, Deps{Session: session}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if len(r.messageHandlers) != 1 {
		t.Errorf("messageHandlers = %d, want 1", len(r.messageHandlers))
	}
	if len(r.interactionHandlers) != 1 {
		t.Errorf("interactionHandlers = %d, want 1", len(r.interactionHandlers))
	}
}

// listenerModule reports one message and one interaction listener.
type listenerModule struct{}

func (m *listenerModule) Name() string                              { return "listener" }
func (m *listenerModule) Init(_ Deps) error                         { return nil }
func (m *listenerModule) Commands() []*discordgo.ApplicationCommand { return nil }
func (m *listenerModule) Listeners() []EventListener {
	return []EventListener{
		func(*discordgo.Session, *discordgo.MessageCreate) {},
		func(*discordgo.Session, *discordgo.InteractionCreate) {},
	}
}
func (m *listenerModule) Components() []ComponentHandler { return nil }
func (m *listenerModule) Background() []BackgroundTask   { return nil }
func (m *listenerModule) Shutdown() error                { return nil }

func TestRouter_MultipleCommands(t *testing.T) {
	r := makeRouter()
	var pingCalled, pongCalled atomic.Bool
	r.AddCommand("ping", func(_ *discordgo.Session, _ *discordgo.InteractionCreate) { pingCalled.Store(true) })
	r.AddCommand("pong", func(_ *discordgo.Session, _ *discordgo.InteractionCreate) { pongCalled.Store(true) })

	r.onInteractionCreate(nil, slashInteraction("ping"))
	r.onInteractionCreate(nil, slashInteraction("pong"))

	if !pingCalled.Load() {
		t.Fatal("ping handler not called")
	}
	if !pongCalled.Load() {
		t.Fatal("pong handler not called")
	}
}
