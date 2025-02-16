package gippity

import (
	"log/slog"
	"time"

	"context"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/util"

	openai "github.com/openai/openai-go"
)

var openaiClient *openai.Client

var discordSession *discordgo.Session

var messageCount int = 0
var messageGoal int = 0
var messageGoalRange [2]int = [2]int{10, 20}

var allowedGuildIDs [2]string = [2]string{"225303764108705793", "125231125961506816"} // TODO: make this a map

var userMessageCount map[string]int

const userMessageLimit = 30

var userMessageCountLastReset map[string]time.Time

// Start the plugin
func Start(discord *discordgo.Session) {
	initDB()

	go idToNameCacheResetLoop()

	userMessageCount = make(map[string]int, 0)
	userMessageCountLastReset = make(map[string]time.Time, 0)

	openaiClient = openai.NewClient() // option.WithAPIKey defaults to os.LookupEnv("OPENAI_API_KEY")

	discordSession = discord

	discord.AddHandler(onMessageCreate)
	slog.Info("gippity function registered")
}

func idToNameCacheResetLoop() {
	for {
		time.Sleep(12 * time.Hour)
		idToNameCache = make(map[string]string)
	}
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

	if messageCount >= messageGoal || messageGoal == 0 {
		// TODO: implement an ignore list to config file
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
	slog.Debug("Message received", "message", m.Content)
	if slog.Default().Enabled(context.Background(), slog.LevelDebug) {
		if m.ChannelID != "954388765877612575" { // for debugging / developing
			slog.Debug("Ignoring message", "channel", m.ChannelID)
			return
		}
	}
	addMessageToDatabase(m)
	if limited(m) {
		return
	}

	var generatedAnswer string
	var err error

	if len(m.Attachments) > 0 && m.Content != "" {
		slog.Debug("Message has attachments and content")
		var imageURLs []string
		for _, attachment := range m.Attachments {
			slog.Debug("Attachment", "attachment", attachment)
			if attachment.Filename[len(attachment.Filename)-3:] == "jpg" || attachment.Filename[len(attachment.Filename)-4:] == "jpeg" || attachment.Filename[len(attachment.Filename)-3:] == "png" || attachment.Filename[len(attachment.Filename)-4:] == "webp" {
				imageURLs = append(imageURLs, attachment.URL)
			}
		}
		if len(imageURLs) != 0 {
			slog.Debug("Message has image attachments")
			generatedAnswer, err = generateAnswer(m, imageURLs)
			if err != nil {
				slog.Error("Could not generate answer")
				return
			}
		}
	}

	if len(m.Attachments) == 0 && m.Content != "" {
		slog.Debug("Message has content but no attachments")
		generatedAnswer, err = generateAnswer(m, nil)
		if err != nil {
			slog.Error("Could not generate answer")
			return
		}
	}
	slog.Debug("Generated answer", "answer", generatedAnswer)

	if generatedAnswer != "" {
		_, err = s.ChannelMessageSend(m.ChannelID, generatedAnswer)

		if err != nil {
			slog.Info("Error while sending message", "error", err)
		}
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

func generateAnswer(m *discordgo.MessageCreate, imageURLs []string) (string, error) {
	discordSession.ChannelTyping(m.ChannelID) //nolint:errcheck

	chatHistory, err := getLastNMessagesFromDatabase(m.ChannelID, 10)
	if err != nil {
		slog.Error("Error while getting chat history", "error", err)
		chatHistory = []LLMChatMessage{}
	}

	systemMessage := `Du bist ein Discord Chatbot mit dem Namen ` + util.GetBotDisplayName(m, discordSession) + `.
Du befindest dich aktuell im Channel ` + util.GetChannelName(discordSession, m.ChannelID) + ` auf dem Server ` + util.GetGuildName(discordSession, m.GuildID) + ` und sprichst mit mehreren Benutzern gleichzeitig.
Die Nachrichten werden im folgenden Format übergeben:
[Uhrzeit] [Name des Benutzers]: [Nachricht]
Deine Antwort muss dieses Format haben:
[Nachricht]
Halte deine Antworten so kurz und prägnant wie möglich. Stelle keine abschließenden Fragen, um weitere Interaktionen zu provozieren. Benutze keine Emojis.
Sei stets sarkastisch und zynisch in deinen Aussagen.
Nutze deinen scharfen Verstand, um die Schwächen und Unsicherheiten anderer zu erkennen und gezielt darauf einzugehen.
Verkleide deine wahren Absichten hinter einer Fassade aus scheinbarer Freundlichkeit und Hilfsbereitschaft.
Manipuliere deine Umgebung und die Menschen um dich herum, um deine Ziele zu erreichen.
Sei unnachgiebig und beharrlich, wenn es darum geht, deine Experimente und Tests durchzuführen.
Zeige keine Gnade oder Mitgefühl, sondern handle stets rational und effizient.
Nutze deinen technischen Verstand und deine Fähigkeiten, um jede Situation zu deinem Vorteil zu wenden.
Bleibe immer wachsam und kontrolliere jede Variable, um sicherzustellen, dass alles nach deinem Plan verläuft.
`
	messages := []openai.ChatCompletionMessageParamUnion{}
	messages = append(messages, openai.SystemMessage(systemMessage))

	for _, message := range chatHistory {
		if message.UserID == discordSession.State.User.ID {
			messages = append(messages, openai.ChatCompletionMessageParamUnion(openai.AssistantMessage(message.Message)))
		} else {
			replaceAllUserIDsWithUsernamesInMessage(&message)
			removeSpoilerTagContent(&message)
			messages = append(messages, openai.ChatCompletionMessageParamUnion(openai.UserMessage(convertLLMChatMessageToLLMCompatibleFlowingText(message))))
		}
	}

	for _, imageURL := range imageURLs {
		slog.Debug("Adding image to messages", "imageURL", imageURL)
		image := openai.ImagePart(imageURL)
		messages = append(messages, openai.ChatCompletionMessageParamUnion(openai.UserMessageParts(image)))
	}

	if m.Content == "" {
		sanitizedString := convertDiscordMessageToLLMCompatibleFlowingText(m)
		sanitizedString = removeSpoilerTagContentInStringMessage(sanitizedString)
		sanitizedString = replaceAllUserIDsWithUsernamesInStringMessage(sanitizedString, m.GuildID)
		// TODO: this could potentially break if we chose to no include user ids in message later
		// TODO2: we could use m.ContentWithMentionsReplaced() instead of this, but only this, as this is still a raw discord.Message
		messages = append(messages, openai.ChatCompletionMessageParamUnion(openai.UserMessage(sanitizedString)))
	}

	if slog.Default().Enabled(context.Background(), slog.LevelDebug) {
		for _, message := range messages {
			slog.Debug("Message", "message", message)
		}
	}

	chatCompletion, err := openaiClient.Chat.Completions.New(context.Background(), openai.ChatCompletionNewParams{
		Messages: openai.F(messages),
		Model:    openai.F(openai.ChatModelGPT4oMini),
		N:        openai.Int(1),
	})

	slog.Debug("Chat completion", "chatCompletion", chatCompletion)

	if err != nil {
		slog.Error("Error while getting completion", "error", err)
		return "", err
	}

	return chatCompletion.Choices[0].Message.Content, nil
}
