package coffee

import (
	"log/slog"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/util"
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

var cl []byte = []byte{0xF0, 0x9F, 0xA4, 0xA1}
var da byte = byte(0b00100101) ^ byte(0x13) ^ byte(0x37)
var mo byte = byte(0b00100000) ^ byte(0x13) ^ byte(0x37)

// Start the plugin
func Start(discord *discordgo.Session) {
	discord.AddHandler(onMessageCreate)
	slog.Info("coffee function registered")
}

func onMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	for _, v := range messages {
		if v == strings.ToLower(m.Content) {
			today := time.Now()
			if today.Day() == int(da) && today.Month() == time.Month(int(mo)) {
				util.ReactOnMessage(s, m.ChannelID, m.ID, string(cl), "add")
			} else {
				if m.Author.ID == "263959699764805642" || m.Author.ID == "217697101818232832" {
					util.ReactOnMessage(s, m.ChannelID, m.ID, "🍵", "add")
				} else {
					util.ReactOnMessage(s, m.ChannelID, m.ID, "☕", "add")
				}
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
