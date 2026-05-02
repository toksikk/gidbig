package coffee

import (
	"log/slog"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/util"
)

const fallbackBeverage = "☕"

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
	discord.AddHandler(onMessageCreate)
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
	for _, v := range messages {
		if v == strings.ToLower(m.Content) {
			if util.IsSpecial() {
				util.ReactOnMessage(s, m.ChannelID, m.ID, string(util.Ae[util.RandomRange(0, len(util.Ae))]), "add")
				util.ReactOnMessage(s, m.ChannelID, m.ID, string(util.Cl), "add")
			} else {
				util.ReactOnMessage(s, m.ChannelID, m.ID, beverageEmojiFor(m.Author.ID), "add")
				// faces
				if m.Author.ID == "269898849714307073" {
					util.ReactOnMessage(s, m.ChannelID, m.ID, ":sidus:576309032789475328", "add")
				}
				if m.Author.ID == "125230846629249024" {
					util.ReactOnMessage(s, m.ChannelID, m.ID, ":sikk:355329009824825355", "add")
				}
			}
		}
	}
}
