package gippity

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/llm"
	"github.com/toksikk/gidbig/internal/util"

	openai "github.com/openai/openai-go/v3"
)

func (m *Module) isLimitedUser(mc *discordgo.MessageCreate) bool {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()

	if _, exists := m.userMessageCount[mc.Author.ID]; !exists {
		m.userMessageCountLastReset[mc.Author.ID] = time.Now()
		m.userMessageCount[mc.Author.ID] = 0
		return false
	}

	if _, exists := m.userMessageCountLastReset[mc.Author.ID]; !exists {
		m.userMessageCountLastReset[mc.Author.ID] = time.Now()
	}

	if int(time.Since(m.userMessageCountLastReset[mc.Author.ID]).Hours()) >= 1 {
		m.userMessageCountLastReset[mc.Author.ID] = time.Now()
		m.userMessageCount[mc.Author.ID] = 0
		return false
	}

	m.userMessageCount[mc.Author.ID]++

	return m.userMessageCount[mc.Author.ID] >= m.userMessageLimit
}

func (m *Module) limited(mc *discordgo.MessageCreate) bool {
	if mc.Author.ID == m.session.State.User.ID {
		return true
	}

	if m.ignoredUserIDs[mc.Author.ID] {
		slog.Info("ignoring message from ignored user", "user", mc.Author.ID)
		return true
	}

	if !m.allowedGuildIDs[mc.GuildID] {
		slog.Info("not using ai generated message in this guild", "guild", mc.GuildID)
		return true
	}

	if m.isMentioned(mc) {
		if m.isLimitedUser(mc) {
			count, reset := m.mentionState(mc.Author.ID)
			slog.Info("not answering because of user limitation", "userMessageCount", count, "userMessageLimit", m.userMessageLimit, "userMessageCountLastReset", reset)
			_, err := m.session.ChannelMessageSend(mc.ChannelID, "Du hast heute schon genug Nachrichten geschrieben. Komm wann anders wieder.")
			if err != nil {
				slog.Info("Error while sending message", "error", err)
			}
			return true
		}
		return false
	}

	return true
}

func (m *Module) onMessageCreate(s *discordgo.Session, mc *discordgo.MessageCreate) {
	slog.Debug("Message received", "message", mc.Content)
	if slog.Default().Enabled(context.Background(), slog.LevelDebug) {
		if mc.ChannelID != "954388765877612575" { // for debugging / developing
			slog.Debug("Ignoring message", "channel", mc.ChannelID)
			return
		}
	}
	m.addMessageToDatabase(mc, m.isMentioned(mc))

	imageURLs := extractImageURLs(mc.Attachments)
	if len(imageURLs) > 0 {
		slog.Debug("Describing image attachments", "count", len(imageURLs))
		description, err := m.describeImagesFunc(imageURLs)
		if err != nil {
			slog.Error("Could not describe images", "error", err)
		} else {
			m.addAttachmentsToDatabase(mc.ID, imageURLs, description)
		}
	}

	if m.limited(mc) {
		return
	}

	var generatedAnswer string
	var err error

	if len(imageURLs) > 0 && mc.Content != "" {
		slog.Debug("Message has image attachments and content")
		generatedAnswer, err = m.generateAnswerFunc(mc, imageURLs)
		if err != nil {
			slog.Error("Could not generate answer")
			return
		}
	}

	if len(mc.Attachments) == 0 && mc.Content != "" {
		slog.Debug("Message has content but no attachments")
		generatedAnswer, err = m.generateAnswerFunc(mc, nil)
		if err != nil {
			slog.Error("Could not generate answer")
			return
		}
	}
	slog.Debug("Generated answer", "answer", generatedAnswer)

	if generatedAnswer != "" {
		_, err = s.ChannelMessageSend(mc.ChannelID, generatedAnswer)

		if err != nil {
			slog.Info("Error while sending message", "error", err)
		}
	}
}

func (m *Module) onMessageUpdate(_ *discordgo.Session, mu *discordgo.MessageUpdate) {
	if mu.Message == nil || mu.Author == nil || mu.Author.Bot {
		return
	}
	if mu.EditedTimestamp == nil || mu.Content == "" {
		return
	}
	if !m.allowedGuildIDs[mu.GuildID] {
		return
	}
	if m.getUserPrivacy(mu.Author.ID) {
		return
	}
	var count int
	if err := m.db.QueryRow(`SELECT COUNT(*) FROM chat_history WHERE message_id = ?`, mu.ID).Scan(&count); err != nil || count == 0 {
		return
	}
	m.addMessageEditToDatabase(mu.ID, mu.Content, mu.EditedTimestamp.Unix())
}

func (m *Module) onGippityInteractionCreate(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionApplicationCommand {
		return
	}
	data := i.ApplicationCommandData()
	if data.Name != "gippity" {
		return
	}
	if len(data.Options) == 0 || data.Options[0].Name != "privacy" {
		return
	}

	privacyOpts := data.Options[0].Options
	if len(privacyOpts) == 0 {
		return
	}
	value := privacyOpts[0].StringValue()
	enabled := value == "on"

	var userID string
	if i.Member != nil {
		userID = i.Member.User.ID
	} else if i.User != nil {
		userID = i.User.ID
	}
	if userID == "" {
		return
	}
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral},
	}); err != nil {
		slog.Error("gippity: failed to defer privacy interaction", "error", err)
		return
	}
	if err := m.setUserPrivacy(userID, enabled); err != nil {
		slog.Error("gippity: failed to set user privacy", "error", err, "userID", userID)
		msg := "Fehler beim Speichern deiner Datenschutzeinstellung."
		_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &msg})
		return
	}

	var msg string
	if enabled {
		msg = "Datenschutz aktiviert: Deine vergangenen Nachrichten werden im KI-Kontext anonymisiert."
	} else {
		msg = "Datenschutz deaktiviert: Deine vergangenen Nachrichten werden im KI-Kontext im Klartext verwendet."
	}
	_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &msg})
}

func (m *Module) isMentioned(mc *discordgo.MessageCreate) bool {
	botUserID := m.session.State.User.ID

	for _, user := range mc.Mentions {
		if user.ID == botUserID {
			return true
		}
	}

	return false
}

func (m *Module) generateAnswer(mc *discordgo.MessageCreate, imageURLs []string) (string, error) {
	m.channelTypingFunc(m.session, mc.ChannelID)

	chatHistory, err := m.getLastNMessagesFromDatabase(mc.ChannelID, 10)
	if err != nil {
		slog.Error("Error while getting chat history", "error", err)
		chatHistory = []LLMChatMessage{}
	}

	reactionSummaries := reactionSummaryCache{}

	systemMessageBase := `Discord-Chatbot, Name ` + util.GetBotDisplayName(mc, m.session) + `.
Channel ` + util.GetChannelName(m.session, mc.ChannelID) + `, Server ` + util.GetGuildName(m.session, mc.GuildID) + `. Mehrere Benutzer gleichzeitig.
Im Channel: ` + util.GetAllMembersOfChannelAsString(m.session, mc.ChannelID) + `.
---
Nachrichten kommen so: [Zeitstempel] [Benutzername]: [Nachricht]
Reaktionen stehen als [reactions: Emoji×Anzahl] am Ende einer Nachricht; nur Anzahl, keine Namen.
---
Deine Antwort nur: [Deine Nachricht]
---
Nicht im Benutzer-Format antworten — nicht mit Zeitstempel beginnen.
---
Keine abschließenden Fragen zum Weiterreden.
---
Manche Namen sind Pseudonyme (Benutzer 1, 2, …) für anonyme Teilnehmer. Echte Identität nicht erraten — nicht aus Kontext, Schreibstil oder anderen Signalen. Anonymisierte Inhalte ersetzt und nicht verfügbar; als opake Kontextnachrichten behandeln.`

	systemMessage := systemMessageBase + "\n" + m.enrichSystemMessage(llm.Personality())

	messages := []openai.ChatCompletionMessageParamUnion{}
	messages = append(messages, openai.SystemMessage(systemMessage))

	if mc.MessageReference != nil {
		refMsg, refErr := m.fetchReferencedMessageFunc(m.session, mc.MessageReference)
		if refErr != nil {
			slog.Warn("gippity: could not fetch referenced message", "error", refErr)
		} else if refMsg != nil {
			content := refMsg.Content
			isBot := refMsg.Author != nil && refMsg.Author.Bot
			if !isBot && refMsg.Author != nil && m.getUserPrivacy(refMsg.Author.ID) {
				content = "[message content hidden -- user opted out]"
			}
			authorName := ""
			if refMsg.Author != nil {
				authorName = refMsg.Author.Username
			}
			note := fmt.Sprintf("[System note: User is replying to a message from %s: %s]", authorName, content)
			refChannelID := refMsg.ChannelID
			if refChannelID == "" && mc.MessageReference != nil {
				refChannelID = mc.MessageReference.ChannelID
			}
			if summary := m.reactionSummary(reactionSummaries, refChannelID, refMsg.ID); summary != "" {
				note += " " + summary
			}
			messages = append(messages, openai.SystemMessage(note))
		}
	}

	pseudonymMap := make(map[string]string)
	pseudonymCounter := 0
	privacyCache := make(map[string]bool)

	for _, message := range chatHistory {
		message.ReactionSummary = m.reactionSummary(reactionSummaries, message.ChannelID, message.MessageID)

		if message.UserID == m.session.State.User.ID {
			content := message.Message
			if message.ReactionSummary != "" {
				content += " " + message.ReactionSummary
			}
			messages = append(messages, openai.ChatCompletionMessageParamUnion(openai.AssistantMessage(content)))
			continue
		}

		if _, cached := privacyCache[message.UserID]; !cached {
			privacyCache[message.UserID] = m.getUserPrivacy(message.UserID)
		}
		if !message.IsBotMention && privacyCache[message.UserID] {
			pseudo, ok := pseudonymMap[message.UserID]
			if !ok {
				pseudonymCounter++
				pseudo = fmt.Sprintf("Benutzer %d", pseudonymCounter)
				pseudonymMap[message.UserID] = pseudo
			}
			anon := LLMChatMessage{
				Username:        pseudo,
				TimestampString: message.TimestampString,
				Message:         "[Anonymisierte Nachricht]",
				ReactionSummary: message.ReactionSummary,
			}
			messages = append(messages, openai.ChatCompletionMessageParamUnion(openai.UserMessage(convertLLMChatMessageToLLMCompatibleFlowingText(anon))))
			continue
		}

		m.replaceAllUserIDsWithUsernamesInMessage(&message)
		removeSpoilerTagContent(&message)
		messages = append(messages, openai.ChatCompletionMessageParamUnion(openai.UserMessage(convertLLMChatMessageToLLMCompatibleFlowingText(message))))
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

	if mc.Content == "" {
		sanitizedString := m.convertDiscordMessageToLLMCompatibleFlowingText(mc)
		sanitizedString = removeSpoilerTagContentInStringMessage(sanitizedString)
		sanitizedString = m.replaceAllUserIDsWithUsernamesInStringMessage(sanitizedString, mc.GuildID)
		// TODO: this could potentially break if we chose to no include user ids in message later
		messages = append(messages, openai.ChatCompletionMessageParamUnion(openai.UserMessage(sanitizedString)))
	}

	if slog.Default().Enabled(context.Background(), slog.LevelDebug) {
		for _, message := range messages {
			slog.Debug("Message", "message", message)
		}
	}

	chatCompletion, err := m.chatCompletionFunc(context.Background(), openai.ChatCompletionNewParams{
		Messages:            messages,
		Model:               llm.Model(),
		N:                   openai.Int(1),
		MaxCompletionTokens: openai.Int(300),
	})

	slog.Debug("Chat completion", "chatCompletion", chatCompletion)

	if err != nil {
		slog.Error("Error while getting completion", "error", err)
		return "", err
	}

	return llm.CompletionContent(chatCompletion)
}
