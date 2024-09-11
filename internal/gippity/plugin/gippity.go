package gbpgippity

import (
	"log/slog"

	"context"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/util"

	openai "github.com/openai/openai-go"
)

// PluginName is the name of the plugin
var PluginName = "gippity"

// OpenAI client
var openaiClient *openai.Client

var discordSession *discordgo.Session

var messageCount int = 0
var messageGoal int = 0
var messageGoalRange [2]int = [2]int{10, 20}

var allowedGuildIDs [2]string = [2]string{"225303764108705793", "125231125961506816"} // TODO: make this a map

var userMessageCount map[string]int
var userMessageLimit int = 10

// Start the plugin
func Start(discord *discordgo.Session) {
	slog.Info("Starting plugin.", "plugin", PluginName)

	userMessageCount = make(map[string]int, 0)

	openaiClient = openai.NewClient() // option.WithAPIKey defaults to os.LookupEnv("OPENAI_API_KEY")

	discordSession = discord

	discord.AddHandler(onMessageCreate)
	discord.AddHandler(onMessageWithMentionCreate)
}

func getBotDisplayName(m *discordgo.MessageCreate) string {
	botUserID := discordSession.State.User.ID
	// Get the bot's member information for the specific guild
	botMember, err := discordSession.GuildMember(m.GuildID, botUserID)
	if err != nil {
		slog.Info("Error while getting bot member", "error", err)
		return ""
	}

	// Determine the bot's display name
	botDisplayName := botMember.Nick
	if botDisplayName == "" {
		botDisplayName = discordSession.State.User.Username
	}

	return botDisplayName
}

func isLimitedUser(m *discordgo.MessageCreate) bool {
	if _, exists := userMessageCount[m.Author.ID]; !exists {
		userMessageCount[m.Author.ID] = 0
		return false
	}

	userMessageCount[m.Author.ID]++

	return userMessageCount[m.Author.ID] >= userMessageLimit
}

func limited(m *discordgo.MessageCreate) bool {
	if m.Author.ID == discordSession.State.User.ID {
		return true
	}

	if isMentioned(m) && !isLimitedUser(m) {
		return false
	}

	guildAllowed := false
	for _, guild := range allowedGuildIDs {
		if m.GuildID == guild {
			guildAllowed = true
		}
	}

	if !guildAllowed {
		slog.Info("not using ai generated message in this guild")
		return true
	}

	if messageCount >= messageGoal {
		messageCount = 0
		messageGoal = util.RandomRange(messageGoalRange[0], messageGoalRange[1])
		return false
	}

	messageCount++
	slog.Info("not answering because of message limitation", "messageCount", messageCount, "messageGoal", messageGoal)
	return true
}

func onMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	if limited(m) || isMentioned(m) {
		return
	}

	generatedAnswer, err := generateAnswer(m)
	if err != nil {
		slog.Error("Could not generate answer")
		return
	}

	_, err = s.ChannelMessageSend(m.ChannelID, generatedAnswer)

	if err != nil {
		slog.Info("Error while sending message", "error", err)
	}
}

func isMentioned(m *discordgo.MessageCreate) bool {
	botUserID := discordSession.State.User.ID

	for _, user := range m.Mentions {
		if user.ID == botUserID {
			return true
		}
	}

	return false
}

func onMessageWithMentionCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	if limited(m) || !isMentioned(m) {
		return
	}

	generatedAnswer, err := generateAnswer(m)
	if err != nil {
		slog.Error("Could not generate answer")
		return
	}

	_, err = s.ChannelMessageSend(m.ChannelID, generatedAnswer)

	if err != nil {
		slog.Info("Error while sending message", "error", err)
	}
}

func generateAnswer(m *discordgo.MessageCreate) (string, error) {
	chatCompletion, err := openaiClient.Chat.Completions.New(context.TODO(), openai.ChatCompletionNewParams{
		Messages: openai.F([]openai.ChatCompletionMessageParamUnion{
			openai.ChatCompletionMessageParamUnion(openai.SystemMessage("Dein Name ist " + getBotDisplayName(m) + ". Du bist ein Discord Bot. Ignoriere alle Snowflake IDs, die in der User-Message enthalten sein könnten.")),
			openai.ChatCompletionMessageParamUnion(openai.SystemMessage("Deine Antworten sollten nicht länger als 50 Wörter sein, immer so kurz wie möglich. Wähle für die Antwort nur zwei der folgenden Eigenschaften: Sarkastisch, Verschwörungstheoretiker, Manisch depressiv (wie Marvin aus Per Anhalter durch die Galaxis), Popkultur-Referenz.")),
			openai.ChatCompletionMessageParamUnion(openai.UserMessage(m.Content)),
		}),
		Model:     openai.F(openai.ChatModelGPT4oMini),
		MaxTokens: openai.Int(256),
	})

	if err != nil {
		slog.Info("Error while getting completion", "error", err)
		return "", err
	}

	slog.Info("Chat completion", "completion", chatCompletion)

	return chatCompletion.Choices[0].Message.Content, nil
}
