package gbpgippity

import (
	"encoding/json"
	"log/slog"
	"os"

	"github.com/bwmarrin/discordgo"
)

type chatMessage struct {
	Username    string `json:"username"`
	UserID      string `json:"user_id"`
	ChannelID   string `json:"channel_id"`
	ChannelName string `json:"channel_name"`
	Timestamp   int64  `json:"timestamp"`
	Message     string `json:"message"`
}

type chatMessageHistory struct {
	ChatMessages   []chatMessage `json:"messages"`
	LongtermMemory string        `json:"longterm_memory"`
}

const maxChatMessagesInHistory = 30
const chatHistoryFilename = "message_history.json"

var chatHistory chatMessageHistory

func loadChatHistory() {
	file, err := os.Open(chatHistoryFilename)
	if err != nil {
		chatHistory = chatMessageHistory{ChatMessages: make([]chatMessage, 0)}
		slog.Warn("Error while loading last messages", "error", err)
		return
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	err = decoder.Decode(&chatHistory)
	if err != nil {
		slog.Warn("Error while loading last messages", "error", err)
	}
}

func saveChatHistory() {
	file, err := os.Create(chatHistoryFilename)
	if err != nil {
		slog.Warn("Error while saving last messages", "error", err)
		return
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	err = encoder.Encode(chatHistory)
	if err != nil {
		slog.Warn("Error while saving last messages", "error", err)
	}
}

func addMessage(m *discordgo.MessageCreate) {
	if m.Author.Bot && m.Author.ID != discordSession.State.User.ID {
		return
	}

	if len(chatHistory.ChatMessages) >= maxChatMessagesInHistory {
		createNewLongtermMemory()
		chatHistory.ChatMessages = chatHistory.ChatMessages[1:]
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

	chatHistory.ChatMessages = append(chatHistory.ChatMessages, chatMessage{
		Username:    author,
		UserID:      m.Author.ID,
		ChannelID:   m.ChannelID,
		ChannelName: channelName,
		Timestamp:   m.Timestamp.Unix(),
		Message:     m.Content,
	})

	saveChatHistory()
}

func getMessageHistoryAsJSON(history chatMessageHistory) (string, error) {
	jsonData, err := json.Marshal(history)
	if err != nil {
		return "", err
	}
	return string(jsonData), nil
}

func getOldestChatMessageAsJSON(history chatMessageHistory) (string, error) {
	jsonData, err := json.Marshal(history.ChatMessages[0])
	if err != nil {
		return "", err
	}
	return string(jsonData), nil
}
