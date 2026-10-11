package gippity

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/llm"
	"github.com/toksikk/gidbig/internal/tool"
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

// eligible reports whether a message may be considered for an AI answer at all.
// It runs before the classifier and before any vision call so that ineligible
// messages never incur an API request.
func (m *Module) eligible(mc *discordgo.MessageCreate) bool {
	if mc == nil || mc.Message == nil || mc.Author == nil || mc.Author.ID == "" {
		return false
	}
	if mc.Author.Bot {
		return false
	}
	if m.ignoredUserIDs[mc.Author.ID] {
		slog.Info("ignoring message from ignored user", "user", mc.Author.ID)
		return false
	}
	if !m.allowedGuildIDs[mc.GuildID] {
		slog.Info("not using ai generated message in this guild", "guild", mc.GuildID)
		return false
	}
	if !supportedMessageType(mc.Type) {
		slog.Debug("gippity: unsupported message type", "type", mc.Type)
		return false
	}
	return true
}

// supportedMessageType reports whether the message type is one the bot may
// answer: ordinary messages, replies and thread starter messages. System
// messages (joins, boosts, pins, …) are ignored.
func supportedMessageType(t discordgo.MessageType) bool {
	switch t {
	case discordgo.MessageTypeDefault, discordgo.MessageTypeReply, discordgo.MessageTypeThreadStarterMessage:
		return true
	default:
		return false
	}
}

// isClassifierLimited reports whether the user has exhausted their hourly
// budget of non-mention classifier calls.
func (m *Module) isClassifierLimited(mc *discordgo.MessageCreate) bool {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()

	userID := mc.Author.ID
	if _, exists := m.classifierCount[userID]; !exists {
		m.classifierCountLastReset[userID] = time.Now()
		m.classifierCount[userID] = 0
		return false
	}

	if int(time.Since(m.classifierCountLastReset[userID]).Hours()) >= 1 {
		m.classifierCountLastReset[userID] = time.Now()
		m.classifierCount[userID] = 0
		return false
	}

	m.classifierCount[userID]++
	return m.classifierCount[userID] >= m.classifierRateLimit
}

const (
	// classifierSystemPrompt keeps the decision deterministic and cheap.
	classifierSystemPrompt = `You decide whether a chat bot should reply to a Discord message that did not mention it. Reply with exactly YES to answer or NO to stay silent. YES only when the message clearly invites a bot reply: a direct question the bot can answer, or a request for help. NO for chatter between humans, reactions, or anything that does not need the bot. Reply with one word only.`
	// classifierMaxTokens bounds the classifier answer to a single short word.
	classifierMaxTokens = 5
)

// classifyShouldRespond asks the LLM whether a non-mention message deserves an
// answer. It fails closed: any provider or parsing error means no answer.
func (m *Module) classifyShouldRespond(ctx context.Context, mc *discordgo.MessageCreate) (bool, error) {
	content := strings.TrimSpace(mc.Content)
	if content == "" {
		return false, nil
	}

	model := m.classifierModel
	if model == "" {
		model = m.llmClient.Model()
	}

	completion, err := m.chatCompletionFunc(ctx, openai.ChatCompletionNewParams{
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(classifierSystemPrompt),
			openai.UserMessage(content),
		},
		Model:               model,
		N:                   openai.Int(1),
		Temperature:         openai.Float(0),
		MaxCompletionTokens: openai.Int(classifierMaxTokens),
	})
	if err != nil {
		return false, err
	}

	answer, err := llm.CompletionContent(completion)
	if err != nil {
		return false, err
	}
	return parseClassifierAnswer(answer)
}

// parseClassifierAnswer maps the classifier output to a decision. Anything but
// an explicit YES/NO is an error so the caller can fail closed.
func parseClassifierAnswer(answer string) (bool, error) {
	switch strings.ToUpper(strings.TrimSpace(answer)) {
	case "YES":
		return true, nil
	case "NO":
		return false, nil
	default:
		return false, fmt.Errorf("gippity: unexpected classifier answer %q", answer)
	}
}

// sendReply delivers a bot reply for the message that triggered it. With
// gippity.reply_in_thread enabled the reply is posted inside a public thread
// started on that message, keeping the channel itself clean; messages that
// already arrive in a thread are answered in place, because Discord does not
// allow threads on threads. When the thread cannot be created the reply falls
// back to the channel so an answer is never dropped.
func (m *Module) sendReply(mc *discordgo.MessageCreate, content string) error {
	if !m.replyInThread || m.channelIsThreadFunc(m.session, mc.ChannelID) {
		_, err := m.messageSendFunc(m.session, mc.ChannelID, content)
		return err
	}

	thread, err := m.messageThreadStartFunc(m.session, mc.ChannelID, mc.ID, &discordgo.ThreadStart{Name: threadName(mc.Content)})
	if err != nil {
		slog.Warn("gippity: could not start thread, replying in channel", "channel", mc.ChannelID, "error", err)
	} else if thread == nil || thread.ID == "" {
		slog.Warn("gippity: thread start returned no channel, replying in channel", "channel", mc.ChannelID)
		err = errors.New("thread start returned no channel")
	}
	if err != nil {
		_, sendErr := m.messageSendFunc(m.session, mc.ChannelID, content)
		return sendErr
	}

	_, err = m.messageSendFunc(m.session, thread.ID, content)
	return err
}

func (m *Module) onMessageCreate(s *discordgo.Session, mc *discordgo.MessageCreate) {
	slog.Debug("Message received", "message", mc.Content)
	if slog.Default().Enabled(context.Background(), slog.LevelDebug) {
		if mc.ChannelID != "954388765877612575" { // for debugging / developing
			slog.Debug("Ignoring message", "channel", mc.ChannelID)
			return
		}
	}

	mentioned := m.isMentioned(mc)
	m.addMessageToDatabase(mc, mentioned)

	if !m.eligible(mc) {
		return
	}

	if mentioned {
		if m.isLimitedUser(mc) {
			count, reset := m.mentionState(mc.Author.ID)
			slog.Info("not answering because of user limitation", "userMessageCount", count, "userMessageLimit", m.userMessageLimit, "userMessageCountLastReset", reset)
			if err := m.sendReply(mc, "Du hast heute schon genug Nachrichten geschrieben. Komm wann anders wieder."); err != nil {
				slog.Info("Error while sending message", "error", err)
			}
			return
		}
	} else {
		// Non-mention responses are opt-in per user and per deployment.
		if !m.respondToAllMessages {
			return
		}
		if !m.getUserConsent(mc.Author.ID) {
			return
		}
		if strings.TrimSpace(mc.Content) == "" {
			return
		}
		if m.isClassifierLimited(mc) {
			slog.Debug("gippity: classifier budget reached, skipping", "user", mc.Author.ID)
			return
		}
		shouldRespond, err := m.classifyFunc(context.Background(), mc)
		if err != nil {
			slog.Warn("gippity: classifier failed, not responding", "error", err)
			return
		}
		if !shouldRespond {
			return
		}
		// A positive decision still consumes the reply budget; skip silently.
		if m.isLimitedUser(mc) {
			slog.Debug("gippity: reply budget reached, skipping", "user", mc.Author.ID)
			return
		}
	}

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
		if err := m.sendReply(mc, generatedAnswer); err != nil {
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
	var count int64
	if err := m.db.Model(&ChatMessage{}).Where("message_id = ?", mu.ID).Count(&count).Error; err != nil || count == 0 {
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
	if len(data.Options) == 0 || len(data.Options[0].Options) == 0 {
		return
	}
	subcommand := data.Options[0].Name
	if subcommand != "privacy" && subcommand != "nonmention" {
		return
	}
	enabled := data.Options[0].Options[0].StringValue() == "on"

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
		slog.Error("gippity: failed to defer interaction", "error", err)
		return
	}

	var err error
	var msg string
	switch subcommand {
	case "privacy":
		err = m.setUserPrivacy(userID, enabled)
		if err != nil {
			msg = "Fehler beim Speichern deiner Datenschutzeinstellung."
			break
		}
		if enabled {
			msg = "Datenschutz aktiviert: Deine vergangenen Nachrichten werden im KI-Kontext anonymisiert."
		} else {
			msg = "Datenschutz deaktiviert: Deine vergangenen Nachrichten werden im KI-Kontext im Klartext verwendet."
		}
	case "nonmention":
		err = m.setUserConsent(userID, enabled)
		if err != nil {
			msg = "Fehler beim Speichern deiner Einstellung."
			break
		}
		if enabled {
			msg = "Non-Mention aktiviert: Der Bot darf entscheiden, ob er auf deine Nachrichten ohne Erwähnung antwortet."
		} else {
			msg = "Non-Mention deaktiviert: Der Bot antwortet nur, wenn du ihn erwähnst."
		}
	}
	if err != nil {
		slog.Error("gippity: failed to save setting", "error", err, "userID", userID, "subcommand", subcommand)
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

// typingKeepAliveInterval is how often the typing indicator is refreshed while
// an answer is being generated. Discord expires the indicator after roughly 10
// seconds, so a slow request (tool calls, long completions) would otherwise
// drop it before the reply is sent.
var typingKeepAliveInterval = 8 * time.Second

// startTypingKeepAlive shows the typing indicator immediately and then keeps
// refreshing it in the background until the returned stop function is called.
// Callers should defer stop so the indicator also ends when generation fails.
// stop is safe to call more than once.
func (m *Module) startTypingKeepAlive(channelID string) func() {
	done := make(chan struct{})
	var once sync.Once
	m.channelTypingFunc(m.session, channelID)
	go func() {
		ticker := time.NewTicker(typingKeepAliveInterval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				m.channelTypingFunc(m.session, channelID)
			}
		}
	}()
	return func() { once.Do(func() { close(done) }) }
}

func (m *Module) generateAnswer(mc *discordgo.MessageCreate, imageURLs []string) (string, error) {
	stopTyping := m.startTypingKeepAlive(mc.ChannelID)
	defer stopTyping()

	chatHistory, err := m.getLastNMessagesFromDatabase(mc.ChannelID, 10)
	if err != nil {
		slog.Error("Error while getting chat history", "error", err)
		chatHistory = []LLMChatMessage{}
	}

	reactionSummaries := reactionSummaryCache{}

	// The member list is offered as the get_channel_members tool when tools are
	// available; otherwise fall back to putting it in the prompt so the model
	// never loses it.
	membersNote := ""
	if !m.channelMembersToolOffered() {
		if members := m.channelMembersFunc(m.session, mc.ChannelID); members != "" {
			membersNote = "Im Channel: " + members + ".\n"
		}
	}

	systemMessageBase := `Discord-Chatbot, Name ` + util.GetBotDisplayName(mc, m.session) + `.
Channel ` + util.GetChannelName(m.session, mc.ChannelID) + `, Server ` + util.GetGuildName(m.session, mc.GuildID) + `. Mehrere Benutzer gleichzeitig.
` + membersNote + `---
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

	systemMessage := systemMessageBase + "\n" + m.enrichSystemMessage(m.llmClient.Personality())

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

	env := tool.Env{GuildID: mc.GuildID, ChannelID: mc.ChannelID}
	if mc.Author != nil {
		env.UserID = mc.Author.ID
	}

	answer, err := m.completeWithTools(context.Background(), env, messages)
	if err != nil {
		slog.Error("Error while getting completion", "error", err)
		return "", err
	}

	return answer, nil
}
