package gbpgippity

import (
	"encoding/json"
	"log/slog"
	"os"

	"github.com/bwmarrin/discordgo"
)

type messageHistory struct {
	Messages []message `json:"messages"`
}

var msgHistory messageHistory

func loadLastMessages() {
	file, err := os.Open(messageHistoryFileName)
	if err != nil {
		msgHistory = messageHistory{Messages: make([]message, 0)}
		slog.Warn("Error while loading last messages", "error", err)
		return
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	err = decoder.Decode(&msgHistory)
	if err != nil {
		slog.Warn("Error while loading last messages", "error", err)
	}
}

func saveLastMessages() {
	file, err := os.Create(messageHistoryFileName)
	if err != nil {
		slog.Warn("Error while saving last messages", "error", err)
		return
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	err = encoder.Encode(msgHistory)
	if err != nil {
		slog.Warn("Error while saving last messages", "error", err)
	}
}

func addMessage(m *discordgo.MessageCreate) {
	if m.Author.Bot && m.Author.ID != discordSession.State.User.ID {
		return
	}

	if len(msgHistory.Messages) >= maxHistoryMessages {
		msgHistory.Messages = msgHistory.Messages[1:]
	}
	author := m.Author.Username
	if m.Member != nil {
		author = m.Member.Nick
	}
	channel, err := discordSession.Channel(m.ChannelID)
	channelName := ""
	if err != nil {
		slog.Info("Error while getting channel", "error", err)
		channelName = m.ChannelID
	} else {
		channelName = channel.Name
	}

	msgHistory.Messages = append(msgHistory.Messages, message{
		Username:    author,
		UserID:      m.Author.ID,
		ChannelID:   m.ChannelID,
		ChannelName: channelName,
		Timestamp:   m.Timestamp.Unix(),
		Message:     m.Content,
	})
	saveLastMessages()
}

func getMessageHistoryAsJSON(history messageHistory) (string, error) {
	jsonData, err := json.Marshal(history)
	if err != nil {
		return "", err
	}
	return string(jsonData), nil
}
