package gbpgippity

import (
	"log/slog"
	"math/rand"
	"time"

	"context"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/util"

	openai "github.com/openai/openai-go"
)

// PluginName is the name of the plugin
var PluginName = "gippity"

var openaiClient *openai.Client

var discordSession *discordgo.Session

var messageCount int = 0
var messageGoal int = 0
var messageGoalRange [2]int = [2]int{10, 20}

var behaviorPool = []string{
	//	"sarkastisch",
	//	"pessimistisch",
	"zynisch",
	"spöttisch",
	//	"ironisch",
	"launisch",
	//	"böse",
	//	"herablassend",
	"nett",
	"freundlich",
	"hilfsbereit",
	"lieb",
	"optimistisch",
	"entspannt",
	"energisch",
	"respektvoll",
	"mürrisch",
	"senil",
	"paranoid",
}

var allowedGuildIDs [2]string = [2]string{"225303764108705793", "125231125961506816"} // TODO: make this a map

var userMessageCount map[string]int

const userMessageLimit = 30

var userMessageCountLastReset map[string]time.Time

// Start the plugin
func Start(discord *discordgo.Session) {
	slog.Info("Starting plugin.", "plugin", PluginName)
	initDB()

	userMessageCount = make(map[string]int, 0)
	userMessageCountLastReset = make(map[string]time.Time, 0)

	openaiClient = openai.NewClient() // option.WithAPIKey defaults to os.LookupEnv("OPENAI_API_KEY")

	discordSession = discord

	discord.AddHandler(onMessageCreate)
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

	if int(time.Since(userMessageCountLastReset[m.Author.ID]).Hours()) >= 1 {
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
		if m.GuildID == "125231125961506816" {
			// they don't want the bot to answer randomly in this guild
			return true
		}
		messageCount = 0
		messageGoal = util.RandomRange(messageGoalRange[0], messageGoalRange[1])
		return false
	}

	messageCount++
	slog.Info("not answering because of message limitation", "messageCount", messageCount, "messageGoal", messageGoal)
	return true
}

func onMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	addMessageToDatabase(m)
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
	discordSession.ChannelTyping(m.ChannelID) //nolint:errcheck
	shuffledBehaviors := behaviorPool
	rand.Shuffle(len(shuffledBehaviors), func(i, j int) {
		shuffledBehaviors[i], shuffledBehaviors[j] = shuffledBehaviors[j], shuffledBehaviors[i]
	})

	responseMentioned := "Diese Nachricht ist nicht an dich direkt gerichtet, aber du antwortest bitte dennoch darauf, damit das Gespräch weitergeführt wird."
	if isMentioned(m) {
		responseMentioned = "Diese Nachricht ist an dich direkt gerichtet."
	}

	grammarBehavior := "Korrigiere niemals Rechtschreib- oder Grammatikfehler."
	if rand.Intn(99) < 10 {
		grammarBehavior = "Mache auf Grammatik und Rechtschreibfehler aufmerksam. Mache dich über den Fehler lustig."
	}

	chatHistory, err := getLastNMessagesFromDatabase(m, 30)
	if err != nil {
		slog.Error("Error while getting chat history", "error", err)
		chatHistory = []LLMChatMessage{}
	}

	systemMessage := `
			Du bist ein Discord Chatbot.
			Du befindest dich aktuell im Channel + ` + util.GetChannelName(discordSession, m.ChannelID) + ` + auf dem Server + ` + util.GetGuildName(discordSession, m.GuildID) + ` und sprichst mit mehreren Benutzern gleichzeitig.
			Die Nachrichten werden im folgenden Format übergeben:
			[Uhrzeit] [Name des Benutzers]: [Nachricht]
			Deine Antwort muss dieses Format haben:
			[Nachricht]
			Antworte so kurz wie möglich.
			Deine Antworten sollen maximal 100 Wörter haben.
			Gestalte deine Antwort nach dieser Verhaltensweise: ` + shuffledBehaviors[0] + `.
			` + grammarBehavior + `
			` + responseMentioned
	messages := []openai.ChatCompletionMessageParamUnion{}
	messages = append(messages, openai.SystemMessage(systemMessage))

	for _, message := range chatHistory {
		if message.UserID == discordSession.State.User.ID {
			messages = append(messages, openai.ChatCompletionMessageParamUnion(openai.AssistantMessage(message.Message)))
		} else {
			replaceAllUserIDsWithUsernamesInMessage(&message)
			messages = append(messages, openai.ChatCompletionMessageParamUnion(openai.UserMessage(convertLLMChatMessageToLLMCompatibleFlowingText(message))))
		}
	}

	// TODO: this could potentially if we chose to no include user ids in message later
	messages = append(messages, openai.ChatCompletionMessageParamUnion(openai.UserMessage(
		replaceAllUserIDsWithUsernamesInStringMessage(
			convertDiscordMessageToLLMCompatibleFlowingText(m), m.GuildID))))

	chatCompletion, err := openaiClient.Chat.Completions.New(context.TODO(), openai.ChatCompletionNewParams{
		Messages: openai.F(messages),
		Model:    openai.F(openai.ChatModelGPT4oMini),
		N:        openai.Int(1),
	})

	if err != nil {
		slog.Error("Error while getting completion", "error", err)
		return "", err
	}

	return chatCompletion.Choices[0].Message.Content, nil
}
