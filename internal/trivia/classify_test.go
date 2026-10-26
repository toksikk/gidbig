package trivia

import (
	"testing"

	"github.com/bwmarrin/discordgo"
)

func TestClassifyMessage(t *testing.T) {
	tests := []struct {
		name string
		msg  *discordgo.Message
		want messageCounts
	}{
		{"nil", nil, messageCounts{}},
		{"empty", &discordgo.Message{}, messageCounts{}},
		{"plain words", &discordgo.Message{Content: "hello there  world"}, messageCounts{Words: 3}},
		{"url stripped from words", &discordgo.Message{Content: "look https://example.com/x now"}, messageCounts{Words: 2, Links: 1}},
		{"gif attachment by type", &discordgo.Message{Attachments: []*discordgo.MessageAttachment{{Filename: "a.bin", ContentType: "image/gif"}}}, messageCounts{GIFs: 1}},
		{"gif attachment by extension", &discordgo.Message{Attachments: []*discordgo.MessageAttachment{{Filename: "a.GIF"}}}, messageCounts{GIFs: 1}},
		{"png attachment", &discordgo.Message{Attachments: []*discordgo.MessageAttachment{{Filename: "a.png", ContentType: "image/png"}}}, messageCounts{Pictures: 1}},
		{"non-image attachment", &discordgo.Message{Attachments: []*discordgo.MessageAttachment{{Filename: "a.pdf", ContentType: "application/pdf"}}}, messageCounts{}},
		{
			"tenor link with gifv embed counted once",
			&discordgo.Message{
				Content: "https://tenor.com/view/abc",
				Embeds:  []*discordgo.MessageEmbed{{URL: "https://tenor.com/view/abc", Type: discordgo.EmbedTypeGifv}},
			},
			messageCounts{GIFs: 1},
		},
		{
			"link with gifv embed reclassified",
			&discordgo.Message{
				Content: "https://example.com/funny",
				Embeds:  []*discordgo.MessageEmbed{{URL: "https://example.com/funny", Type: discordgo.EmbedTypeGifv}},
			},
			messageCounts{GIFs: 1},
		},
		{
			"youtube link with video embed",
			&discordgo.Message{
				Content: "https://youtube.com/watch?v=1",
				Embeds:  []*discordgo.MessageEmbed{{URL: "https://youtube.com/watch?v=1", Type: discordgo.EmbedTypeVideo}},
			},
			messageCounts{Links: 1},
		},
		{"giphy subdomain", &discordgo.Message{Content: "https://media.giphy.com/x"}, messageCounts{GIFs: 1}},
		{
			"mixed",
			&discordgo.Message{
				Content:     "two words https://a.example https://tenor.com/v",
				Attachments: []*discordgo.MessageAttachment{{Filename: "p.jpg", ContentType: "image/jpeg"}, {Filename: "g.gif"}},
			},
			messageCounts{Words: 2, GIFs: 2, Pictures: 1, Links: 1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyMessage(tt.msg); got != tt.want {
				t.Errorf("classifyMessage() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
