package bot

import (
	"sync/atomic"
	"testing"

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

func TestRouter_MessageCreateIsNoop(t *testing.T) {
	r := makeRouter()
	r.onMessageCreate(nil, &discordgo.MessageCreate{})
}

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
