package gbpgippity

import (
	"encoding/json"
	"log/slog"
	"regexp"
	"strings"

	"github.com/toksikk/gidbig/internal/util"
)

// nolint:unused
func convertLLMChatMessageToJSON(message LLMChatMessage) string {
	json, err := json.Marshal(message)
	if err != nil {
		slog.Error("Error while converting LLMChatMessage to JSON", "error", err)
		return ""
	}
	return string(json)
}

func convertLLMChatMessageToLLMCompatibleFlowingText(message LLMChatMessage) string {
	return message.TimestampString + " " + message.Username + ": " + message.Message
}

func replaceAllUserIDsWithUsernamesInMessage(message *LLMChatMessage) {
	regexp := regexp.MustCompile(`<@!?(\\d+)>`)
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
