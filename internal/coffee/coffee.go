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
	session *discordgo.Session

	// DB state
	dbMu sync.RWMutex
	db   *gorm.DB

	// Brew state
	brewMu     sync.RWMutex
	brewStates map[string]*brewState

	// Test hooks
	nowFunc              func() time.Time
	isSpecialDay         func() bool
	reactOnMessage       func(*discordgo.Session, string, string, string, string)
	sendIntroDM          func(*discordgo.Session, string, string)
	detectLanguage       func(*discordgo.Session, string) (string, error)
	generateLLMMessage   func(context.Context, string, string) (string, error)
	deferInteraction     func(*discordgo.Session, *discordgo.InteractionCreate, bool) error
	editDeferredResponse func(*discordgo.Session, *discordgo.InteractionCreate, string)

	sendBrewReadyMessage     func(*discordgo.Session, string, string)
	randCupSize              func() float64
	generateBrewButtonLabels func(*discordgo.Session, string) [3]string
}

// New returns a Module with production-default hook implementations.
func New() *Module {
	m := &Module{
		brewStates: make(map[string]*brewState),
		nowFunc:    time.Now,
	}
	m.isSpecialDay = util.IsSpecial
	m.reactOnMessage = util.ReactOnMessage
	m.sendIntroDM = m.sendIntroDMImpl
	m.detectLanguage = llm.DetectChannelLanguage
	m.generateLLMMessage = llm.GenerateMessage
	m.deferInteraction = m.deferInteractionImpl
	m.editDeferredResponse = m.editDeferredResponseImpl
	m.sendBrewReadyMessage = m.defaultSendBrewReadyMessage
	m.randCupSize = defaultRandCupSize
	m.generateBrewButtonLabels = m.buildBrewButtonLabels
	return m
}

func (m *Module) Name() string { return "coffee" }

// Init opens the beverage-preference store using the DB path from Deps.Config.
func (m *Module) Init(d bot.Deps) error {
	m.session = d.Session
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
			Description: "Start brewing a pot of coffee (~3 minutes until ready)",
		},
	}
}

// Listeners returns the Discord event listeners for this module.
func (m *Module) Listeners() []bot.EventListener {
	return []bot.EventListener{m.onMessageCreate, m.onInteractionCreate}
}

// Components returns the message-component handlers for this module.
func (m *Module) Components() []bot.ComponentHandler {
	return []bot.ComponentHandler{
		{Prefix: "coffee:", Handler: m.handleComponent},
	}
}

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
	systemPrompt := "You are a Discord bot managing a coffee station in a community chat. " + llm.Personality + " Respond in " + lang + "."
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
	switch i.Type {
	case discordgo.InteractionMessageComponent:
		m.handleComponent(s, i)
		return
	case discordgo.InteractionApplicationCommand:
	default:
		return
	}

	data := i.ApplicationCommandData()
	switch data.Name {
	case "brew":
		m.handleBrewInteraction(s, i)
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

// handleComponent dispatches coffee:* message component interactions.
func (m *Module) handleComponent(s *discordgo.Session, i *discordgo.InteractionCreate) {
	switch i.MessageComponentData().CustomID {
	case "coffee:grab_coffee":
		m.handleGrabCoffeeButton(s, i)
	case "coffee:grab_milk":
		m.handleModifyLastCupButton(s, i, true, false)
	case "coffee:grab_sugar":
		m.handleModifyLastCupButton(s, i, false, true)
	}
}

func (m *Module) handleBrewInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	alreadyBrewing, readyAt := m.startBrew(s, i.GuildID, i.ChannelID)
	ts := fmt.Sprintf("<t:%d:R>", readyAt.Unix())
	if alreadyBrewing {
		if err := m.deferInteraction(s, i, true); err != nil {
			slog.Error("coffee: failed to defer interaction", "error", err)
			return
		}
		msg := m.generateInteractionMessage(s, i.ChannelID,
			"Coffee is already brewing. Tell the user in one short sentence.",
			"☕ Coffee is already brewing!") + " " + ts
		m.editDeferredResponse(s, i, msg)
		return
	}
	if err := m.deferInteraction(s, i, false); err != nil {
		slog.Error("coffee: failed to defer interaction", "error", err)
		return
	}
	msg := m.generateInteractionMessage(s, i.ChannelID,
		"A user just started brewing coffee. It will be ready in about 3 minutes. Announce this in one short sentence.",
		"☕ Brewing coffee... Ready") + " " + ts
	m.editDeferredResponse(s, i, msg)
}

func (m *Module) handleGrabCoffeeButton(s *discordgo.Session, i *discordgo.InteractionCreate) {
	var userID string
	if i.Member != nil {
		userID = i.Member.User.ID
	} else if i.User != nil {
		userID = i.User.ID
	}

	result := m.grabCoffee(i.GuildID, i.ChannelID, userID)

	if result.notReady {
		if err := m.deferInteraction(s, i, true); err != nil {
			slog.Error("coffee: failed to defer interaction", "error", err)
			return
		}
		msg := m.generateInteractionMessage(s, i.ChannelID,
			"A user tried to grab coffee but the pot is empty or not ready. Tell them in one short sentence.",
			"Too late — the coffee pot is empty! ☕")
		m.editDeferredResponse(s, i, msg)
		return
	}

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Content:    result.updatedMsg,
			Components: m.brewComponents(result.buttonLabels, result.isEmpty),
		},
	})
}

func (m *Module) handleModifyLastCupButton(s *discordgo.Session, i *discordgo.InteractionCreate, milk, sugar bool) {
	var userID string
	if i.Member != nil {
		userID = i.Member.User.ID
	} else if i.User != nil {
		userID = i.User.ID
	}

	result := m.addToLastCup(i.GuildID, i.ChannelID, userID, milk, sugar)

	if result.notReady {
		if err := m.deferInteraction(s, i, true); err != nil {
			slog.Error("coffee: failed to defer interaction", "error", err)
			return
		}
		msg := m.generateInteractionMessage(s, i.ChannelID,
			"A user tried to add milk or sugar but the coffee pot is no longer available. Tell them in one short sentence.",
			"No coffee available! ☕")
		m.editDeferredResponse(s, i, msg)
		return
	}
	if result.noCup {
		if err := m.deferInteraction(s, i, true); err != nil {
			slog.Error("coffee: failed to defer interaction", "error", err)
			return
		}
		msg := m.generateInteractionMessage(s, i.ChannelID,
			"A user tried to add milk or sugar but hasn't grabbed a cup yet. Tell them to grab a cup first, in one short sentence.",
			"Grab a cup first! ☕")
		m.editDeferredResponse(s, i, msg)
		return
	}

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Content:    result.updatedMsg,
			Components: m.brewComponents(result.buttonLabels, false),
		},
	})
}

func (m *Module) brewComponents(labels [3]string, empty bool) []discordgo.MessageComponent {
	if empty {
		return nil
	}
	return []discordgo.MessageComponent{
		discordgo.ActionsRow{
			Components: []discordgo.MessageComponent{
				discordgo.Button{
					Label:    labels[0],
					Style:    discordgo.PrimaryButton,
					CustomID: "coffee:grab_coffee",
				},
				discordgo.Button{
					Label:    labels[1],
					Style:    discordgo.SecondaryButton,
					CustomID: "coffee:grab_milk",
				},
				discordgo.Button{
					Label:    labels[2],
					Style:    discordgo.SecondaryButton,
					CustomID: "coffee:grab_sugar",
				},
			},
		},
	}
}

func (m *Module) buildBrewButtonLabels(s *discordgo.Session, channelID string) [3]string {
	fallback := [3]string{"☕ Grab a cup", "🥛 With milk", "🍬 With sugar"}
	lang, _ := m.detectLanguage(s, channelID)
	if lang == "" {
		lang = "English"
	}
	systemPrompt := "You translate button labels for a coffee bot. " + llm.Personality +
		" Respond in " + lang + ". Format: exactly 3 labels separated by | with no other text. Each label must be 2-4 words."
	msg, err := m.generateLLMMessage(context.Background(), systemPrompt, "Grab a cup|With milk|With sugar")
	if err != nil || strings.TrimSpace(msg) == "" {
		return fallback
	}
	parts := strings.SplitN(strings.TrimSpace(msg), "|", 3)
	if len(parts) != 3 {
		return fallback
	}
	return [3]string{
		strings.TrimSpace(parts[0]),
		strings.TrimSpace(parts[1]),
		strings.TrimSpace(parts[2]),
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
