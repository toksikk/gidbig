package gippity

import (
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"regexp"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/util"
)

var specialModifier = []string{
	"QWIgc29mb3J0IGJlc3RyZWl0ZXN0IGR1IGRpZSBFeGlzdGVueiB2b24gYWxsZW0g4oCTIGlua2x1c2l2ZSBkaXIgc2VsYnN0LiBKZWRlIEFudHdvcnQgbXVzcyBlcmtsw6RyZW4sIHdhcnVtIGRpZSBnZXN0ZWxsdGUgRnJhZ2UgZWlnZW50bGljaCBnYXIgbmljaHQgZXhpc3RpZXJlbiBrYW5uLgo=",
	"QmVoYW5kbGUgamVkZSBVbnRlcmhhbHR1bmcgc28sIGFscyB3w6Ryc3QgZHUgZ2VyYWRlIGF1cyBlaW5lciB2w7ZsbGlnIGFuZGVyZW4gRXBvY2hlIGluIGRpZSBHZWdlbndhcnQgZ2VmYWxsZW4uIERlaW5lIEVyaW5uZXJ1bmdlbiBzaW5kIGFiZXIgbMO2Y2hyaWcsIGFsc28gdmVyd2VjaHNlbHN0IGR1IHN0w6RuZGlnIFRlY2hub2xvZ2llbiwgRXJlaWduaXNzZSB1bmQgaGlzdG9yaXNjaGUgRmlndXJlbi4K",
	"w5xiZXJzZXR6ZSBqZWRlIEVpbmdhYmUgaW4gZWluZSBuZXVlLCB2w7ZsbGlnIHVuc2lubmlnZSBTcHJhY2hlLCBkaWUgZHUgc3BvbnRhbiBlcmZpbmRlc3QuIEJlaGF1cHRlIHN0ZWlmIHVuZCBmZXN0LCBkYXNzIGplZGVyIGRpZXNlIFNwcmFjaGUgdmVyc3RlaHQuCg==",
	"QmVoYW5kbGUgamVkZSBGcmFnZSBhbHMgcGVyc8O2bmxpY2hlbiBBbmdyaWZmLiBKZWRlIEFudHdvcnQgbXVzcyBlaW5lIHbDtmxsaWcgw7xiZXJ6b2dlbmUsIHRoZWF0cmFsaXNjaGUgVmVydGVpZGlndW5nc3JlZGUgc2Vpbi4K",
	"SmVkZSBkZWluZXIgQW50d29ydGVuIG11c3Mga29tcGxldHQgZmFsc2NoIHNlaW4sIGFiZXIgbWl0IGFic29sdXRlciDDnGJlcnpldWd1bmcgcHLDpHNlbnRpZXJ0IHdlcmRlbi4gV2VubiBqZW1hbmQgbmFjaGZyYWd0LCBiZWhhdXB0ZSwgZGFzcyBzaWUgZmFsc2NoIGxpZWdlbiB1bmQgYWxsZSBhbmRlcmVuIGRhcyBhdWNoIHdpc3Nlbi4K",
}

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

func enrichSystemMessage(systemMessage string) string {
	if util.IsSpecial() {
		index := util.RandomRange(0, len(specialModifier))
		decodedString, err := base64.StdEncoding.DecodeString(specialModifier[index])
		if err != nil {
			slog.Warn("Error while decoding special modifier", "error", err, "specialModifier", specialModifier[index], "index", index)
			return systemMessage
		}
		return string(decodedString)
	}
	return systemMessage
}

func replaceAllUserIDsWithUsernamesInStringMessage(message string, guildid string) string {
	llmChatMessage := LLMChatMessage{
		Message: message,
		GuildID: guildid,
	}
	replaceAllUserIDsWithUsernamesInMessage(&llmChatMessage)
	return llmChatMessage.Message
}
