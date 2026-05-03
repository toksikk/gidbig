package coffee

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/util"
)

const fallbackBeverage = "☕"

var (
	discordSession    *discordgo.Session
	registeredCommand *discordgo.ApplicationCommand
	isSpecialDay      = util.IsSpecial
	reactOnMessage    = util.ReactOnMessage
	sendIntroDM       = sendIntroDMFunc
)

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
	slog.Info("coffee function registered")
}

// Shutdown deletes the registered application command and closes the beverage preference store.
func Shutdown() {
	if discordSession != nil && registeredCommand != nil {
		if err := discordSession.ApplicationCommandDelete(discordSession.State.User.ID, "", registeredCommand.ID); err != nil {
			slog.Error("coffee: failed to delete setbeverage command", "error", err)
		}
		registeredCommand = nil
	}
	closeStore()
}

func onMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
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
	if i.Type != discordgo.InteractionApplicationCommand {
		return
	}
	data := i.ApplicationCommandData()
	if data.Name != "setbeverage" {
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

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: fmt.Sprintf("Your morning beverage is now %s ☑️", emoji),
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})

	if !introducedBefore {
		sendIntroDM(s, userID, emoji)
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
