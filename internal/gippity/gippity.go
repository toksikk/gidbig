package gippity

import (
	"log/slog"
	"time"

	"context"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/cfg"
	"github.com/toksikk/gidbig/internal/llm"
	"github.com/toksikk/gidbig/internal/util"

	openai "github.com/openai/openai-go/v3"
)

var discordSession *discordgo.Session

var generateAnswerFunc = generateAnswer

var (
	allowedGuildIDs map[string]bool
	ignoredUserIDs  map[string]bool
)

var userMessageCount map[string]int

const userMessageLimit = 30

var userMessageCountLastReset map[string]time.Time

// Start the plugin
func Start(discord *discordgo.Session) {
	initDB()

	go idToNameCacheResetLoop()

	userMessageCount = make(map[string]int, 0)
	userMessageCountLastReset = make(map[string]time.Time, 0)

	config := cfg.GetConfig()
	allowedGuildIDs = make(map[string]bool)
	ignoredUserIDs = make(map[string]bool)

	for _, id := range config.Gippity.AllowedGuilds {
		allowedGuildIDs[id] = true
	}
	for _, id := range config.Gippity.IgnoredUsers {
		ignoredUserIDs[id] = true
	}

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

	if ignoredUserIDs[m.Author.ID] {
		slog.Info("ignoring message from ignored user", "user", m.Author.ID)
		return true
	}

	if !allowedGuildIDs[m.GuildID] {
		slog.Info("not using ai generated message in this guild", "guild", m.GuildID)
		return true
	}

	if isMentioned(m) {
		if isLimitedUser(m) {
			slog.Info("not answering because of user limitation", "userMessageCount", userMessageCount[m.Author.ID], "userMessageLimit", userMessageLimit, "userMessageCountLastReset", userMessageCountLastReset[m.Author.ID])
			_, err := discordSession.ChannelMessageSend(m.ChannelID, "Du hast heute schon genug Nachrichten geschrieben. Komm wann anders wieder.")
			if err != nil {
				slog.Info("Error while sending message", "error", err)
			}
			return true
		}
		return false
	}

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
			generatedAnswer, err = generateAnswerFunc(m, imageURLs)
			if err != nil {
				slog.Error("Could not generate answer")
				return
			}
		}
	}

	if len(m.Attachments) == 0 && m.Content != "" {
		slog.Debug("Message has content but no attachments")
		generatedAnswer, err = generateAnswerFunc(m, nil)
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

	systemMessageBase := `Du bist ein Discord Chatbot mit dem Namen ` + util.GetBotDisplayName(m, discordSession) + `.
Du befindest dich aktuell im Channel ` + util.GetChannelName(discordSession, m.ChannelID) + ` auf dem Server ` + util.GetGuildName(discordSession, m.GuildID) + ` und sprichst mit mehreren Benutzern gleichzeitig.
Im Channel sind: ` + util.GetAllMembersOfChannelAsString(discordSession, m.ChannelID) + `.
---
Die Nachrichten werden im folgenden Format übergeben:
[Zeitstempel der Nachricht] [Name des Benutzers]: [Nachricht des Benutzers]
---
Deine Antwort muss dieses Format haben:
[Deine Nachricht]
---
Achte darauf, dass deine Antwort nicht im gleichen Format wie die Benutzernachrichten ist, also nicht mit einem Zeitstempel beginnt.
---
Stelle keine abschließenden Fragen, um weitere Interaktionen zu provozieren.`

	systemMessage := systemMessageBase + "\n" + enrichSystemMessage(llm.Personality)

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
		imageParam := openai.ChatCompletionContentPartImageImageURLParam{
			URL: imageURL,
		}
		imageContent := openai.ImageContentPart(imageParam)
		userMessage := openai.ChatCompletionUserMessageParam{
			Content: openai.ChatCompletionUserMessageParamContentUnion{
				OfArrayOfContentParts: []openai.ChatCompletionContentPartUnionParam{
					imageContent,
				},
			},
		}
		messages = append(messages, openai.ChatCompletionMessageParamUnion{
			OfUser: &userMessage,
		})
	}

	if m.Content == "" {
		sanitizedString := convertDiscordMessageToLLMCompatibleFlowingText(m)
		sanitizedString = removeSpoilerTagContentInStringMessage(sanitizedString)
		sanitizedString = replaceAllUserIDsWithUsernamesInStringMessage(sanitizedString, m.GuildID)
		// TODO: this could potentially break if we chose to no include user ids in message later
		messages = append(messages, openai.ChatCompletionMessageParamUnion(openai.UserMessage(sanitizedString)))
	}

	if slog.Default().Enabled(context.Background(), slog.LevelDebug) {
		for _, message := range messages {
			slog.Debug("Message", "message", message)
		}
	}

	chatCompletion, err := llm.GetClient().Chat.Completions.New(context.Background(), openai.ChatCompletionNewParams{
		Messages:  messages,
		Model:     openai.ChatModelGPT4oMini,
		N:         openai.Int(1),
		MaxTokens: openai.Int(300),
	})

	slog.Debug("Chat completion", "chatCompletion", chatCompletion)

	if err != nil {
		slog.Error("Error while getting completion", "error", err)
		return "", err
	}

	return chatCompletion.Choices[0].Message.Content, nil
}
