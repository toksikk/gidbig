package gippity

import (
	"database/sql"
	"log/slog"

	"github.com/bwmarrin/discordgo"
	// sqlite3 driver
	_ "github.com/mattn/go-sqlite3"
	"github.com/toksikk/gidbig/internal/util"
)

// ChatMessage represents a chat message
type ChatMessage struct {
	UserID    string
	ChannelID string
	Timestamp int64
	MessageID string
	Message   string
	GuildID   string
}

// LLMChatMessage represents a chat message in a human readable format
type LLMChatMessage struct {
	UserID          string
	Username        string
	ChannelID       string
	ChannelName     string
	Timestamp       int64
	TimestampString string
	MessageID       string
	Message         string
	GuildID         string
	GuildName       string
}

const chatHistoryDBFilename = "gippity.db"

var database *sql.DB
var idToNameCache = make(map[string]string)

func initDB() {
	var err error
	database, err = sql.Open("sqlite3", chatHistoryDBFilename)
	if err != nil {
		slog.Error("Error while opening database", "error", err)
		return
	}

	_, err = database.Exec(`CREATE TABLE IF NOT EXISTS chat_history (user_id text, channel_id text, timestamp integer, message text, message_id text, guild_id text)`)
	if err != nil {
		slog.Error("Error while creating table", "error", err)
	}
}

func addMessageToDatabase(m *discordgo.MessageCreate) {
	stmt, err := database.Prepare("INSERT INTO chat_history (user_id, channel_id, timestamp, message, message_id, guild_id) VALUES (?, ?, ?, ?, ?, ?)")
	if err != nil {
		slog.Error("Error while preparing statement", "error", err)
		return
	}
	defer stmt.Close()

	_, err = stmt.Exec(m.Author.ID, m.ChannelID, util.GetTimestampOfMessage(m.ID).Unix(), m.Content, m.ID, m.GuildID)
	if err != nil {
		slog.Error("Error while inserting message into database", "error", err)
	}
}

func getLastNMessagesFromDatabase(channelID string, n int) ([]LLMChatMessage, error) {
	stmt, err := database.Prepare(`
	SELECT user_id, channel_id, timestamp, message, message_id, guild_id
	FROM (
	    SELECT user_id, channel_id, timestamp, message, message_id, guild_id
	    FROM chat_history
	    WHERE channel_id = ?
	    ORDER BY timestamp DESC
	    LIMIT ?
    ) AS subquery
	ORDER BY timestamp ASC;
	`)
	if err != nil {
		slog.Error("Error while preparing statement", "error", err)
		return nil, err
	}
	defer stmt.Close()

	rows, err := stmt.Query(channelID, n)
	if err != nil {
		slog.Error("Error while querying database", "error", err)
		return nil, err
	}
	defer rows.Close()

	llmMessages := make([]LLMChatMessage, 0, n)
	for rows.Next() {
		var message LLMChatMessage
		err = rows.Scan(
			&message.UserID,
			&message.ChannelID,
			&message.Timestamp,
			&message.Message,
			&message.MessageID,
			&message.GuildID,
		)
		if err != nil {
			slog.Error("Error while scanning row", "error", err)
			return nil, err
		}

		if idToNameCache[message.UserID] == "" {
			idToNameCache[message.UserID] = util.GetUsernameForUserIDInGuild(discordSession, message.UserID, message.GuildID)
		}
		if idToNameCache[message.ChannelID] == "" {
			idToNameCache[message.ChannelID] = util.GetChannelName(discordSession, message.ChannelID)
		}
		if idToNameCache[message.GuildID] == "" {
			idToNameCache[message.GuildID] = util.GetGuildName(discordSession, message.GuildID)
		}

		message.Username = idToNameCache[message.UserID]
		message.ChannelName = idToNameCache[message.ChannelID]
		message.GuildName = idToNameCache[message.GuildID]
		message.TimestampString = util.GetTimestampOfMessage(message.MessageID).Format("2006-01-02 15:04:05")

		llmMessages = append(llmMessages, message)
	}

	return llmMessages, nil
}
