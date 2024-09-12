package gbpgippity

import (
	"log/slog"
	"time"

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

var lastMessage []*discordgo.MessageCreate

const maxMessages = 20

var messageCount int = 0
var messageGoal int = 0
var messageGoalRange [2]int = [2]int{10, 20}

var allowedGuildIDs [2]string = [2]string{"225303764108705793", "125231125961506816"} // TODO: make this a map

var userMessageCount map[string]int

const userMessageLimit = 20

var userMessageCountLastReset map[string]time.Time

// Start the plugin
func Start(discord *discordgo.Session) {
	slog.Info("Starting plugin.", "plugin", PluginName)

	userMessageCount = make(map[string]int, 0)
	userMessageCountLastReset = make(map[string]time.Time, 0)

	openaiClient = openai.NewClient() // option.WithAPIKey defaults to os.LookupEnv("OPENAI_API_KEY")

	discordSession = discord

	discord.AddHandler(onMessageCreate)
}

func hoursSince(t time.Time) int {
	return int(time.Since(t).Hours())
}

func addMessage(m *discordgo.MessageCreate) {
	if len(lastMessage) >= maxMessages {
		lastMessage = lastMessage[1:]
	}
	lastMessage = append(lastMessage, m)
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
		userMessageCountLastReset[m.Author.ID] = time.Now()
		userMessageCount[m.Author.ID] = 0
		return false
	}

	if _, exists := userMessageCountLastReset[m.Author.ID]; !exists {
		userMessageCountLastReset[m.Author.ID] = time.Now()
	}

	if hoursSince(userMessageCountLastReset[m.Author.ID]) >= 1 {
		userMessageCountLastReset[m.Author.ID] = time.Now()
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

	if isMentioned(m) && isLimitedUser(m) {
		slog.Info("not answering because of user limitation", "userMessageCount", userMessageCount[m.Author.ID], "userMessageLimit", userMessageLimit, "userMessageCountLastReset", userMessageCountLastReset[m.Author.ID])
		_, err := discordSession.ChannelMessageSend(m.ChannelID, "Du hast heute schon genug Nachrichten geschrieben. Komm wann anders wieder.")
		if err != nil {
			slog.Info("Error while sending message", "error", err)
		}
		return true
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
	addMessage(m)

	if limited(m) {
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

func generateAnswer(m *discordgo.MessageCreate) (string, error) {
	lastMessagesAsOneString := ""
	for _, message := range lastMessage[0 : len(lastMessage)-1] {
		lastMessagesAsOneString += "====Author==== " + message.Author.Username
		lastMessagesAsOneString += "====Nachricht==== " + message.Content + "|"
	}
	chatCompletion, err := openaiClient.Chat.Completions.New(context.TODO(), openai.ChatCompletionNewParams{
		Messages: openai.F([]openai.ChatCompletionMessageParamUnion{
			openai.ChatCompletionMessageParamUnion(openai.SystemMessage("Dein Name ist " + getBotDisplayName(m) + ". Du bist ein Discord Bot. Ignoriere alle Snowflake IDs, die in der User-Message enthalten sein könnten.")),
			openai.ChatCompletionMessageParamUnion(openai.SystemMessage("Die letzten Nachrichten waren: " + lastMessagesAsOneString)),
			openai.ChatCompletionMessageParamUnion(openai.SystemMessage("Antworte so kurz wie möglich. Deine Antworten sollen maximal 100 Wörter haben. Starte immer ohne Füllwörter oder Interjektionen. Verwende gelegentlich folgende Eigenschaften, ohne sie wörtlich zu erwähnen: Sarkasmus, Esoterik, manisch-depressive Stimmung, starke und kreative Beleidigungen, Buzzwords aus dem agilen Projektmanagement. Nutze diese Eigenschaften nur, wenn es zum Kontext passt, aber nicht in jeder Antwort. Bei Bedarf, nimm Bezug auf die letzten Nachricht.")),
			openai.ChatCompletionMessageParamUnion(openai.UserMessage(m.Content)),
		}),
		Model:     openai.F(openai.ChatModelGPT4oMini),
		MaxTokens: openai.Int(512),
		N:         openai.Int(1),
	})

	if err != nil {
		slog.Info("Error while getting completion", "error", err)
		return "", err
	}

	slog.Info("Chat completion", "completion", chatCompletion)

	return chatCompletion.Choices[0].Message.Content, nil
}
