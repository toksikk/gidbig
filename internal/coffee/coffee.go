package coffee

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/llm"
	"github.com/toksikk/gidbig/internal/util"
)

const fallbackBeverage = "☕"

type beverage struct {
	name     string
	response string
}

var beverages = []beverage{
	{"tea", "🍵 Tea. Yeah, this counts too."},
	{"mate", "🧉 Mate. For those who haven't fallen asleep yet."},
}

func findBeverage(name string) (beverage, bool) {
	for _, bev := range beverages {
		if bev.name == name {
			return bev, true
		}
	}
	return beverage{}, false
}

func availableBeverageNames() string {
	names := make([]string, 0, len(beverages)+1)
	names = append(names, "coffee")
	for _, bev := range beverages {
		names = append(names, bev.name)
	}
	return strings.Join(names, ", ")
}

var (
	isSpecialDay         = util.IsSpecial
	reactOnMessage       = util.ReactOnMessage
	sendIntroDM          = sendIntroDMFunc
	detectLanguage       = llm.DetectChannelLanguage
	generateLLMMessage   = llm.GenerateMessage
	deferInteraction     = deferInteractionImpl
	editDeferredResponse = editDeferredResponseImpl
)

func deferInteractionImpl(s *discordgo.Session, i *discordgo.InteractionCreate, ephemeral bool) error {
	var flags discordgo.MessageFlags
	if ephemeral {
		flags = discordgo.MessageFlagsEphemeral
	}
	return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Flags: flags},
	})
}

func editDeferredResponseImpl(s *discordgo.Session, i *discordgo.InteractionCreate, content string) {
	if _, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &content}); err != nil {
		slog.Error("coffee: failed to edit deferred response", "error", err)
	}
}

// generateInteractionMessage detects the channel language and uses the LLM to generate
// a response for the given scenario. Returns fallback on any error.
func generateInteractionMessage(s *discordgo.Session, channelID, scenario, fallback string) string {
	lang, _ := detectLanguage(s, channelID)
	if lang == "" {
		lang = "English"
	}
	systemPrompt := "You are a Discord bot managing a coffee station in a community chat. " + llm.Personality + " Respond in " + lang + "."
	msg, err := generateLLMMessage(context.Background(), systemPrompt, scenario)
	if err != nil || strings.TrimSpace(msg) == "" {
		return fallback
	}
	return strings.TrimSpace(msg)
}

func beverageEmojiFor(userID string) string {
	if emoji, ok := getBeverageEmoji(userID); ok {
		return emoji
	}
	return fallbackBeverage
}

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

// Commands returns the slash command definitions for this plugin.
func Commands() []*discordgo.ApplicationCommand {
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
			Description: "Brew something: coffee (timer + grab buttons), or tea/mate (instant)",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "beverage",
					Description: "What to brew: coffee (default), tea, mate",
					Required:    false,
				},
			},
		},
	}
}

// Start the plugin
func Start(discord *discordgo.Session) {
	generateBrewButtonLabels = buildBrewButtonLabels
	discord.AddHandler(onMessageCreate)
	discord.AddHandler(onInteractionCreate)
	if err := openStore("coffee.db"); err != nil {
		slog.Error("coffee: failed to open store", "error", err)
	}
	slog.Info("coffee function registered")
}

// Shutdown closes the beverage preference store.
func Shutdown() {
	closeStore()
}

func onMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author == nil || m.Author.Bot {
		return
	}

	for _, v := range messages {
		if v == strings.ToLower(m.Content) {
			if hasGreetedToday(m.Author.ID) {
				return
			}

			emoji := beverageEmojiFor(m.Author.ID)
			if isSpecialDay() {
				reactOnMessage(s, m.ChannelID, m.ID, string(util.Ae[util.RandomRange(0, len(util.Ae))]), "add")
				reactOnMessage(s, m.ChannelID, m.ID, string(util.Cl), "add")
			} else {
				reactOnMessage(s, m.ChannelID, m.ID, emoji, "add")
				// faces
				if m.Author.ID == "269898849714307073" {
					reactOnMessage(s, m.ChannelID, m.ID, ":sidus:576309032789475328", "add")
				}
				if m.Author.ID == "125230846629249024" {
					reactOnMessage(s, m.ChannelID, m.ID, ":sikk:355329009824825355", "add")
				}
			}

			if !isUserIntroduced(m.Author.ID) {
				sendIntroDM(s, m.Author.ID, emoji)
				if err := markUserIntroduced(m.Author.ID); err != nil {
					slog.Error("coffee: failed to mark user as introduced", "error", err, "userID", m.Author.ID)
				}
			}

			if err := recordGreeting(m.Author.ID); err != nil {
				slog.Error("coffee: failed to record daily greeting", "error", err, "userID", m.Author.ID)
			}
			return
		}
	}
}

func onInteractionCreate(s *discordgo.Session, i *discordgo.InteractionCreate) {
	switch i.Type {
	case discordgo.InteractionMessageComponent:
		switch i.MessageComponentData().CustomID {
		case "grab_coffee":
			handleGrabCoffeeButton(s, i)
		case "grab_milk":
			handleModifyLastCupButton(s, i, true, false)
		case "grab_sugar":
			handleModifyLastCupButton(s, i, false, true)
		}
		return
	case discordgo.InteractionApplicationCommand:
	default:
		return
	}

	data := i.ApplicationCommandData()
	switch data.Name {
	case "brew":
		handleBrewInteraction(s, i)
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

	introducedBefore := isUserIntroduced(userID)

	if err := setBeverageEmoji(userID, emoji); err != nil {
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

	if err := deferInteraction(s, i, true); err != nil {
		slog.Error("coffee: failed to defer interaction", "error", err)
		return
	}
	confirmMsg := generateInteractionMessage(s, i.ChannelID,
		fmt.Sprintf("Confirm to the user that their morning beverage is now set to %s.", emoji),
		fmt.Sprintf("Your morning beverage is now %s ☑️", emoji))
	editDeferredResponse(s, i, confirmMsg)

	if !introducedBefore {
		sendIntroDM(s, userID, emoji)
	}
}

func handleBrewInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	name := ""
	if opts := i.ApplicationCommandData().Options; len(opts) > 0 {
		name = strings.ToLower(strings.TrimSpace(opts[0].StringValue()))
	}

	if name != "" && name != "coffee" {
		if bev, ok := findBeverage(name); ok {
			_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseChannelMessageWithSource,
				Data: &discordgo.InteractionResponseData{Content: bev.response},
			})
			return
		}
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: fmt.Sprintf("I don't know that one. Try: %s", availableBeverageNames()),
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	alreadyBrewing, readyAt := startBrew(s, i.GuildID, i.ChannelID)
	ts := fmt.Sprintf("<t:%d:R>", readyAt.Unix())
	if alreadyBrewing {
		if err := deferInteraction(s, i, true); err != nil {
			slog.Error("coffee: failed to defer interaction", "error", err)
			return
		}
		msg := generateInteractionMessage(s, i.ChannelID,
			"Coffee is already brewing. Tell the user in one short sentence.",
			"☕ Coffee is already brewing!") + " " + ts
		editDeferredResponse(s, i, msg)
		return
	}
	if err := deferInteraction(s, i, false); err != nil {
		slog.Error("coffee: failed to defer interaction", "error", err)
		return
	}
	msg := generateInteractionMessage(s, i.ChannelID,
		"A user just started brewing coffee. It will be ready in about 3 minutes. Announce this in one short sentence.",
		"☕ Brewing coffee... Ready") + " " + ts
	editDeferredResponse(s, i, msg)
}

func handleGrabCoffeeButton(s *discordgo.Session, i *discordgo.InteractionCreate) {
	var userID string
	if i.Member != nil {
		userID = i.Member.User.ID
	} else if i.User != nil {
		userID = i.User.ID
	}

	result := grabCoffee(i.GuildID, i.ChannelID, userID)

	if result.notReady {
		if err := deferInteraction(s, i, true); err != nil {
			slog.Error("coffee: failed to defer interaction", "error", err)
			return
		}
		msg := generateInteractionMessage(s, i.ChannelID,
			"A user tried to grab coffee but the pot is empty or not ready. Tell them in one short sentence.",
			"Too late — the coffee pot is empty! ☕")
		editDeferredResponse(s, i, msg)
		return
	}

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Content:    result.updatedMsg,
			Components: brewComponents(result.buttonLabels, result.isEmpty),
		},
	})
}

// handleModifyLastCupButton adds milk or sugar to the user's most recent cup without
// pouring a new one. Shows an ephemeral error if the user has no cup in this brew.
func handleModifyLastCupButton(s *discordgo.Session, i *discordgo.InteractionCreate, milk, sugar bool) {
	var userID string
	if i.Member != nil {
		userID = i.Member.User.ID
	} else if i.User != nil {
		userID = i.User.ID
	}

	result := addToLastCup(i.GuildID, i.ChannelID, userID, milk, sugar)

	if result.notReady {
		if err := deferInteraction(s, i, true); err != nil {
			slog.Error("coffee: failed to defer interaction", "error", err)
			return
		}
		msg := generateInteractionMessage(s, i.ChannelID,
			"A user tried to add milk or sugar but the coffee pot is no longer available. Tell them in one short sentence.",
			"No coffee available! ☕")
		editDeferredResponse(s, i, msg)
		return
	}
	if result.noCup {
		if err := deferInteraction(s, i, true); err != nil {
			slog.Error("coffee: failed to defer interaction", "error", err)
			return
		}
		msg := generateInteractionMessage(s, i.ChannelID,
			"A user tried to add milk or sugar but hasn't grabbed a cup yet. Tell them to grab a cup first, in one short sentence.",
			"Grab a cup first! ☕")
		editDeferredResponse(s, i, msg)
		return
	}

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Content:    result.updatedMsg,
			Components: brewComponents(result.buttonLabels, false),
		},
	})
}

func brewComponents(labels [3]string, empty bool) []discordgo.MessageComponent {
	if empty {
		return nil
	}
	return []discordgo.MessageComponent{
		discordgo.ActionsRow{
			Components: []discordgo.MessageComponent{
				discordgo.Button{
					Label:    labels[0],
					Style:    discordgo.PrimaryButton,
					CustomID: "grab_coffee",
				},
				discordgo.Button{
					Label:    labels[1],
					Style:    discordgo.SecondaryButton,
					CustomID: "grab_milk",
				},
				discordgo.Button{
					Label:    labels[2],
					Style:    discordgo.SecondaryButton,
					CustomID: "grab_sugar",
				},
			},
		},
	}
}

func buildBrewButtonLabels(s *discordgo.Session, channelID string) [3]string {
	fallback := [3]string{"☕ Grab a cup", "🥛 With milk", "🍬 With sugar"}
	lang, _ := detectLanguage(s, channelID)
	if lang == "" {
		lang = "English"
	}
	systemPrompt := "You translate button labels for a coffee bot. " + llm.Personality +
		" Respond in " + lang + ". Format: exactly 3 labels separated by | with no other text. Each label must be 2-4 words."
	msg, err := generateLLMMessage(context.Background(), systemPrompt, "Grab a cup|With milk|With sugar")
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

func sendIntroDMFunc(s *discordgo.Session, userID string, emoji string) {
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
