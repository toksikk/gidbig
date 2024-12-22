package gbpgippity

import (
	"encoding/json"
	"log/slog"
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
	return message.Username + " schrieb in " + message.ChannelName + " in " + message.GuildName + " um " + message.TimestampString + ": " + message.Message
}
