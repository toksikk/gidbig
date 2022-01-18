package coffee

import (
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/sirupsen/logrus"
)

var messages []string = []string{"moin", "hi", "morgen", "morgn", "guten morgen", "servus", "servas", "dere", "oida", "porst", "prost"}

func OnMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	for _, v := range messages {
		if v == strings.ToLower(m.Content) {
			err := s.MessageReactionAdd(m.ChannelID, m.ID, "☕")
			if err != nil {
				logrus.Info(err)
			}
		}
	}
}
