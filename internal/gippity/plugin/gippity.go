package gbpgippity

import (
	"log/slog"

	"context"

	"github.com/bwmarrin/discordgo"

	openai "github.com/openai/openai-go"
)

// PluginName is the name of the plugin
var PluginName = "gippity"

// OpenAI client
var openaiClient *openai.Client

// Start the plugin
func Start(discord *discordgo.Session) {
	slog.Info("Starting plugin.", "plugin", PluginName)

	openaiClient = openai.NewClient() // option.WithAPIKey defaults to os.LookupEnv("OPENAI_API_KEY")

	discord.AddHandler(onMessageCreate)
}

func onMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	if len(m.Content) < 9 {
		return
	}
	// Get the bot's user ID
	botUserID := s.State.User.ID

	// Get the bot's member information for the specific guild
	botMember, err := s.GuildMember(m.GuildID, botUserID)
	if err != nil {
		slog.Info("Error while getting bot member", "error", err)
		return
	}

	// Determine the bot's display name
	botDisplayName := botMember.Nick
	if botDisplayName == "" {
		botDisplayName = s.State.User.Username
	}

	if m.Author.ID == "125230846629249024" && m.Content[:8] == "!gippity" {
		chatCompletion, err := openaiClient.Chat.Completions.New(context.TODO(), openai.ChatCompletionNewParams{
			Messages: openai.F([]openai.ChatCompletionMessageParamUnion{
				openai.ChatCompletionMessageParamUnion(openai.SystemMessage("Du bist ein sarkastischer Bot und dein Name ist " + botDisplayName + ". Deine Antworten sollten nicht länger als 50 Wörter sein, immer so kurz wie möglich. Beziehe dich manchmal, aber sehr selten, auf die Gaming-Kultur und ihre Memes. Sei manchmal ein lustiger Verschwörungstheoretiker, aber manchmal auch einfach manisch depressiv, wie Marvin aus Per Anhalter durch die Galaxis. Antworte immer in der Sprache, in der du kontaktiert wirst.")),
				openai.ChatCompletionMessageParamUnion(openai.UserMessage(m.Content[9:])),
			}),
			Model:     openai.F(openai.ChatModelGPT4oMini),
			MaxTokens: openai.Int(256),
		})

		if err != nil {
			slog.Info("Error while getting completion", "error", err)
			return
		}

		slog.Info("Chat completion", "completion", chatCompletion)

		_, err = s.ChannelMessageSend(m.ChannelID, chatCompletion.Choices[0].Message.Content)

		if err != nil {
			slog.Info("Error while sending message", "error", err)
		}
	}
}
