package coffee

import (
	"log/slog"
	"strings"

	"github.com/bwmarrin/discordgo"
)

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
	slog.Info("coffee function registered")
}

func onMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	for _, v := range messages {
		if v == strings.ToLower(m.Content) {
			if m.Author.ID == "263959699764805642" || m.Author.ID == "217697101818232832" {
				go s.MessageReactionAdd(m.ChannelID, m.ID, "🍵") // nolint:errcheck
			} else {
				go s.MessageReactionAdd(m.ChannelID, m.ID, "☕") // nolint:errcheck
			}

			// faces
			if m.Author.ID == "269898849714307073" {
				go s.MessageReactionAdd(m.ChannelID, m.ID, ":sidus:576309032789475328") // nolint:errcheck
			}
			if m.Author.ID == "125230846629249024" {
				go s.MessageReactionAdd(m.ChannelID, m.ID, ":sikk:355329009824825355") // nolint:errcheck
			}
		}
	}
}
