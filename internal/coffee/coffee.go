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

var (
	discordSession     *discordgo.Session
	registeredCommand  *discordgo.ApplicationCommand
	registeredBrewCmd  *discordgo.ApplicationCommand
	isSpecialDay       = util.IsSpecial
	reactOnMessage     = util.ReactOnMessage
	sendIntroDM        = sendIntroDMFunc
	detectLanguage     = llm.DetectChannelLanguage
	generateLLMMessage = llm.GenerateMessage
)

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

// Start the plugin
func Start(discord *discordgo.Session) {
	discordSession = discord
	generateBrewButtonLabels = buildBrewButtonLabels
	discord.AddHandler(onMessageCreate)
	discord.AddHandler(onInteractionCreate)
	if err := openStore("coffee.db"); err != nil {
		slog.Error("coffee: failed to open store", "error", err)
	}
	cmd, err := discord.ApplicationCommandCreate(discord.State.User.ID, "", &discordgo.ApplicationCommand{
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
	})
	if err != nil {
		slog.Error("coffee: failed to register setbeverage command", "error", err)
	} else {
		registeredCommand = cmd
	}
	brewCmd, err := discord.ApplicationCommandCreate(discord.State.User.ID, "", &discordgo.ApplicationCommand{
		Name:        "brew",
		Description: "Start brewing a pot of coffee (~3 minutes until ready)",
	})
	if err != nil {
		slog.Error("coffee: failed to register brew command", "error", err)
	} else {
		registeredBrewCmd = brewCmd
	}
	slog.Info("coffee function registered")
}

// Shutdown deletes the registered application commands and closes the beverage preference store.
func Shutdown() {
	if discordSession != nil && registeredCommand != nil {
		if err := discordSession.ApplicationCommandDelete(discordSession.State.User.ID, "", registeredCommand.ID); err != nil {
			slog.Error("coffee: failed to delete setbeverage command", "error", err)
		}
		registeredCommand = nil
	}
	if discordSession != nil && registeredBrewCmd != nil {
		if err := discordSession.ApplicationCommandDelete(discordSession.State.User.ID, "", registeredBrewCmd.ID); err != nil {
			slog.Error("coffee: failed to delete brew command", "error", err)
		}
		registeredBrewCmd = nil
	}
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
			handleGrabCoffeeButton(s, i, false, false)
		case "grab_milk":
			handleGrabCoffeeButton(s, i, true, false)
		case "grab_sugar":
			handleGrabCoffeeButton(s, i, false, true)
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

	confirmMsg := generateInteractionMessage(s, i.ChannelID,
		fmt.Sprintf("Confirm to the user that their morning beverage is now set to %s.", emoji),
		fmt.Sprintf("Your morning beverage is now %s ☑️", emoji))
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: confirmMsg,
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})

	if !introducedBefore {
		sendIntroDM(s, userID, emoji)
	}
}

func handleBrewInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	alreadyBrewing, readyAt := startBrew(s, i.GuildID, i.ChannelID)
	ts := fmt.Sprintf("<t:%d:R>", readyAt.Unix())
	if alreadyBrewing {
		msg := generateInteractionMessage(s, i.ChannelID,
			"Coffee is already brewing. Tell the user in one short sentence.",
			"☕ Coffee is already brewing!") + " " + ts
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: msg,
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}
	msg := generateInteractionMessage(s, i.ChannelID,
		"A user just started brewing coffee. It will be ready in about 3 minutes. Announce this in one short sentence.",
		"☕ Brewing coffee... Ready") + " " + ts
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: msg,
		},
	})
}

func handleGrabCoffeeButton(s *discordgo.Session, i *discordgo.InteractionCreate, milk, sugar bool) {
	var userID string
	if i.Member != nil {
		userID = i.Member.User.ID
	} else if i.User != nil {
		userID = i.User.ID
	}

	result := grabCoffee(i.GuildID, i.ChannelID, userID, milk, sugar)

	if result.notReady {
		msg := generateInteractionMessage(s, i.ChannelID,
			"A user tried to grab coffee but the pot is empty or not ready. Tell them in one short sentence.",
			"Too late — the coffee pot is empty! ☕")
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: msg,
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	// Keep buttons while the pot still has coffee; remove them when empty.
	var components []discordgo.MessageComponent
	if !result.isEmpty {
		components = []discordgo.MessageComponent{
			discordgo.ActionsRow{
				Components: []discordgo.MessageComponent{
					discordgo.Button{
						Label:    result.buttonLabels[0],
						Style:    discordgo.PrimaryButton,
						CustomID: "grab_coffee",
					},
					discordgo.Button{
						Label:    result.buttonLabels[1],
						Style:    discordgo.SecondaryButton,
						CustomID: "grab_milk",
					},
					discordgo.Button{
						Label:    result.buttonLabels[2],
						Style:    discordgo.SecondaryButton,
						CustomID: "grab_sugar",
					},
				},
			},
		}
	}

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Content:    result.updatedMsg,
			Components: components,
		},
	})
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
