package coffee

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/bot"
	"github.com/toksikk/gidbig/internal/llm"
	"github.com/toksikk/gidbig/internal/util"
	"gorm.io/gorm"
)

const fallbackBeverage = "☕"

var messages = []string{
	"moin",
	"hi",
	"morgen",
	"morgn",
	"guten morgen",
	"servus",
	"servas",
	"dere",
	"oida",
	"porst",
	"prost",
	"grias di",
	"gude",
	"spinotwachtldroha",
	"scheipi",
	"heisl",
	"gschissana",
	"christkindl",
}

// Module implements bot.Module and bot.AdminProvider for the coffee plugin.
type Module struct {
	// DB state
	dbMu sync.RWMutex
	db   *gorm.DB

	// machineMu serializes mutations to the per-guild machine inventory.
	machineMu sync.Mutex

	// Test hooks
	nowFunc              func() time.Time
	isSpecialDay         func() bool
	reactOnMessage       func(*discordgo.Session, string, string, string, string)
	sendIntroDM          func(*discordgo.Session, string, string)
	detectLanguage       func(*discordgo.Session, string) (string, error)
	generateLLMMessage   func(context.Context, string, string) (string, error)
	deferInteraction     func(*discordgo.Session, *discordgo.InteractionCreate, bool) error
	editDeferredResponse func(*discordgo.Session, *discordgo.InteractionCreate, string)
}

// New returns a Module with production-default hook implementations.
func New() *Module {
	m := &Module{
		nowFunc: time.Now,
	}
	m.isSpecialDay = util.IsSpecial
	m.reactOnMessage = util.ReactOnMessage
	m.sendIntroDM = m.sendIntroDMImpl
	m.detectLanguage = llm.DetectChannelLanguage
	m.generateLLMMessage = llm.GenerateMessage
	m.deferInteraction = m.deferInteractionImpl
	m.editDeferredResponse = m.editDeferredResponseImpl
	return m
}

// Name returns the module's identifier.
func (m *Module) Name() string { return "coffee" }

// Init opens the beverage-preference store using the DB path from Deps.Config.
func (m *Module) Init(d bot.Deps) error {
	dbPath := "gidbig.db"
	if d.Config != nil && d.Config.Database.Path != "" {
		dbPath = d.Config.Database.Path
	}
	if err := m.openStore(dbPath); err != nil {
		return fmt.Errorf("coffee: open store: %w", err)
	}
	slog.Info("coffee: initialized")
	return nil
}

// Commands returns the slash command definitions for this plugin.
func (m *Module) Commands() []*discordgo.ApplicationCommand {
	return []*discordgo.ApplicationCommand{
		{
			Name:        "setbeverage",
			Description: "Set your preferred morning beverage emoji",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "emoji",
					Description: "The emoji to react with on morning greetings (e.g. 🧃, 🍺, 🫖)",
					Required:    true,
				},
			},
		},
		{
			Name:        "brew",
			Description: "Get a fresh drink from the coffee machine",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "drink",
					Description: "What to brew (default: coffee)",
					Required:    false,
					Choices:     drinkChoices(),
				},
				{
					Type:        discordgo.ApplicationCommandOptionBoolean,
					Name:        "milk",
					Description: "Add a splash of milk (ignored for milk-based drinks)",
					Required:    false,
				},
				{
					Type:        discordgo.ApplicationCommandOptionBoolean,
					Name:        "sugar",
					Description: "Add sugar",
					Required:    false,
				},
			},
		},
		{
			Name:        "coffeemachine",
			Description: "Manage the coffee machine",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "refill",
					Description: "Refill a bean hopper or tank to the top",
					Options: []*discordgo.ApplicationCommandOption{
						{
							Type:        discordgo.ApplicationCommandOptionString,
							Name:        "part",
							Description: "Which tank or hopper to refill",
							Required:    true,
							Choices:     refillChoices(),
						},
					},
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "empty",
					Description: "Empty the coffee grounds container",
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "status",
					Description: "Show machine levels and stats",
				},
			},
		},
	}
}

// drinkChoices builds the /brew drink option choices from the menu.
func drinkChoices() []*discordgo.ApplicationCommandOptionChoice {
	choices := make([]*discordgo.ApplicationCommandOptionChoice, 0, len(menu))
	for _, r := range menu {
		choices = append(choices, &discordgo.ApplicationCommandOptionChoice{Name: r.label, Value: r.key})
	}
	return choices
}

// refillChoices builds the /coffeemachine refill part option choices.
func refillChoices() []*discordgo.ApplicationCommandOptionChoice {
	choices := make([]*discordgo.ApplicationCommandOptionChoice, 0, len(refillParts))
	for _, p := range refillParts {
		choices = append(choices, &discordgo.ApplicationCommandOptionChoice{Name: p.label, Value: p.key})
	}
	return choices
}

// Listeners returns the Discord event listeners for this module.
func (m *Module) Listeners() []bot.EventListener {
	return []bot.EventListener{m.onMessageCreate, m.onInteractionCreate}
}

// Components returns no message-component handlers for this module.
func (m *Module) Components() []bot.ComponentHandler { return nil }

// Background returns no background tasks.
func (m *Module) Background() []bot.BackgroundTask { return nil }

// Shutdown closes the beverage-preference store.
func (m *Module) Shutdown() error {
	return m.closeStore()
}

func (m *Module) deferInteractionImpl(s *discordgo.Session, i *discordgo.InteractionCreate, ephemeral bool) error {
	var flags discordgo.MessageFlags
	if ephemeral {
		flags = discordgo.MessageFlagsEphemeral
	}
	return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Flags: flags},
	})
}

func (m *Module) editDeferredResponseImpl(s *discordgo.Session, i *discordgo.InteractionCreate, content string) {
	if _, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &content}); err != nil {
		slog.Error("coffee: failed to edit deferred response", "error", err)
	}
}

func (m *Module) generateInteractionMessage(s *discordgo.Session, channelID, scenario, fallback string) string {
	lang, _ := m.detectLanguage(s, channelID)
	if lang == "" {
		lang = "English"
	}
	systemPrompt := "Discord bot running a coffee station in a community chat. " + llm.Personality() + " Respond in " + lang + "."
	msg, err := m.generateLLMMessage(context.Background(), systemPrompt, scenario)
	if err != nil || strings.TrimSpace(msg) == "" {
		return fallback
	}
	return strings.TrimSpace(msg)
}

func (m *Module) beverageEmojiFor(userID string) string {
	if emoji, ok := m.getBeverageEmoji(userID); ok {
		return emoji
	}
	return fallbackBeverage
}

func (m *Module) onMessageCreate(s *discordgo.Session, mc *discordgo.MessageCreate) {
	if mc.Author == nil || mc.Author.Bot {
		return
	}

	for _, v := range messages {
		if v == strings.ToLower(mc.Content) {
			if m.hasGreetedToday(mc.Author.ID) {
				return
			}

			emoji := m.beverageEmojiFor(mc.Author.ID)
			if m.isSpecialDay() {
				m.reactOnMessage(s, mc.ChannelID, mc.ID, string(util.Ae[util.RandomRange(0, len(util.Ae))]), "add")
				m.reactOnMessage(s, mc.ChannelID, mc.ID, string(util.Cl), "add")
			} else {
				m.reactOnMessage(s, mc.ChannelID, mc.ID, emoji, "add")
				if mc.Author.ID == "269898849714307073" {
					m.reactOnMessage(s, mc.ChannelID, mc.ID, ":sidus:576309032789475328", "add")
				}
				if mc.Author.ID == "125230846629249024" {
					m.reactOnMessage(s, mc.ChannelID, mc.ID, ":sikk:355329009824825355", "add")
				}
			}

			if !m.isUserIntroduced(mc.Author.ID) {
				m.sendIntroDM(s, mc.Author.ID, emoji)
				if err := m.markUserIntroduced(mc.Author.ID); err != nil {
					slog.Error("coffee: failed to mark user as introduced", "error", err, "userID", mc.Author.ID)
				}
			}

			if err := m.recordGreeting(mc.Author.ID); err != nil {
				slog.Error("coffee: failed to record daily greeting", "error", err, "userID", mc.Author.ID)
			}
			return
		}
	}
}

func (m *Module) onInteractionCreate(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionApplicationCommand {
		return
	}

	data := i.ApplicationCommandData()
	switch data.Name {
	case "brew":
		m.handleBrewInteraction(s, i)
		return
	case "coffeemachine":
		m.handleMachineInteraction(s, i)
		return
	case "setbeverage":
	default:
		return
	}

	emoji := data.Options[0].StringValue()

	if !isValidBeverageEmoji(emoji) {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: fmt.Sprintf("%q is not a valid emoji. Please provide a single emoji or a Discord custom emoji.", emoji),
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	var userID string
	if i.Member != nil {
		userID = i.Member.User.ID
	} else if i.User != nil {
		userID = i.User.ID
	}

	introducedBefore := m.isUserIntroduced(userID)

	if err := m.setBeverageEmoji(userID, emoji); err != nil {
		slog.Error("coffee: failed to set beverage emoji", "error", err, "userID", userID)
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "Failed to save your preference. Please try again later.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	if err := m.deferInteraction(s, i, true); err != nil {
		slog.Error("coffee: failed to defer interaction", "error", err)
		return
	}
	confirmMsg := m.generateInteractionMessage(s, i.ChannelID,
		fmt.Sprintf("Confirm to the user that their morning beverage is now set to %s.", emoji),
		fmt.Sprintf("Your morning beverage is now %s ☑️", emoji))
	m.editDeferredResponse(s, i, confirmMsg)

	if !introducedBefore {
		m.sendIntroDM(s, userID, emoji)
	}
}

// interactionUserID extracts the invoking user's ID from an interaction,
// whether it arrived from a guild member or a DM user.
func interactionUserID(i *discordgo.InteractionCreate) string {
	if i.Member != nil && i.Member.User != nil {
		return i.Member.User.ID
	}
	if i.User != nil {
		return i.User.ID
	}
	return ""
}

// respond sends a single immediate message response to an interaction.
func (m *Module) respond(s *discordgo.Session, i *discordgo.InteractionCreate, content string, ephemeral bool) {
	var flags discordgo.MessageFlags
	if ephemeral {
		flags = discordgo.MessageFlagsEphemeral
	}
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Content: content, Flags: flags},
	}); err != nil {
		slog.Error("coffee: respond failed", "error", err)
	}
}

func (m *Module) sendIntroDMImpl(s *discordgo.Session, userID string, emoji string) {
	channel, err := s.UserChannelCreate(userID)
	if err != nil {
		slog.Error("coffee: failed to create DM channel", "error", err, "userID", userID)
		return
	}

	content := fmt.Sprintf("Your morning beverage is now %s ☑️\n\nWhenever you say 'moin', 'hallo' or similar, I'll greet you with your preferred beverage! You can change this anytime using the `/setbeverage` command. Enjoy your morning! ☀️", emoji)
	_, err = s.ChannelMessageSend(channel.ID, content)
	if err != nil {
		slog.Error("coffee: failed to send intro DM", "error", err, "userID", userID)
	}
}
