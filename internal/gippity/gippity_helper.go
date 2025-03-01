package gippity

import (
	"encoding/json"
	"log/slog"
	"regexp"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/util"
)

// nolint:unused
// convertLLMChatMessageToJSON was used for testing.
// The bot sometimes replied with oddly formatted replies that looked like a message formatted to him.
func convertLLMChatMessageToJSON(message LLMChatMessage) string {
	json, err := json.Marshal(message)
	if err != nil {
		slog.Error("Error while converting LLMChatMessage to JSON", "error", err)
		return ""
	}
	return string(json)
}

func convertLLMChatMessageToLLMCompatibleFlowingText(message LLMChatMessage) string {
	return `
	` + message.TimestampString + `
	` + message.Username + `
	` + message.Message + `
	`
}

func convertDiscordMessageToLLMCompatibleFlowingText(m *discordgo.MessageCreate) string {
	if idToNameCache[m.Author.ID] == "" {
		idToNameCache[m.Author.ID] = util.GetUsernameInGuild(discordSession, m)
	}
	llmChatMessage := LLMChatMessage{
		Message:         m.Message.Content,
		Username:        idToNameCache[m.Author.ID],
		TimestampString: m.Timestamp.Format("2006-01-02 15:04:05"),
	}
	return convertLLMChatMessageToLLMCompatibleFlowingText(llmChatMessage)
}

func removeSpoilerTagContent(message *LLMChatMessage) {
	regexp := regexp.MustCompile("\\|\\|[^|]+\\|\\|") // nolint:gosimple
	message.Message = regexp.ReplaceAllString(message.Message, "||Spoiler||")
}

func removeSpoilerTagContentInStringMessage(message string) string {
	llmChatMessage := LLMChatMessage{
		Message: message,
	}
	removeSpoilerTagContent(&llmChatMessage)
	return llmChatMessage.Message
}

func replaceAllUserIDsWithUsernamesInMessage(message *LLMChatMessage) {
	regexp := regexp.MustCompile("<@!?(\\d+)>") // nolint:gosimple
	matches := regexp.FindAllStringSubmatch(message.Message, -1)
	idToName := make(map[string]string)

	for _, match := range matches {
		fullMatch := match[0] // The full match, e.g., "<@266646297707020289>"
		userID := match[1]    // The captured user ID, e.g., "266646297707020289"

		if idToName[userID] == "" {
			username := util.GetUsernameForUserIDInGuild(discordSession, userID, message.GuildID)
			if username == "" {
				username = "Unbekannter Benutzer"
			}
			idToName[fullMatch] = username
		}
	}

	// Replace each full match with the corresponding username
	for fullMatch, username := range idToName {
		message.Message = strings.ReplaceAll(message.Message, fullMatch, username)
	}
}

func replaceAllUserIDsWithUsernamesInStringMessage(message string, guildid string) string {
	llmChatMessage := LLMChatMessage{
		Message: message,
		GuildID: guildid,
	}
	replaceAllUserIDsWithUsernamesInMessage(&llmChatMessage)
	return llmChatMessage.Message
}
