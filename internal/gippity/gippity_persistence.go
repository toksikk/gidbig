package gippity

import (
	"database/sql"
	"log/slog"
	"strings"

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
	UserID            string
	Username          string
	ChannelID         string
	ChannelName       string
	Timestamp         int64
	TimestampString   string
	MessageID         string
	Message           string
	GuildID           string
	GuildName         string
	IsBotMention      bool
	ImageDescriptions []string
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
		slog.Error("Error while creating chat_history table", "error", err)
	}

	// idempotent: ignore error if column already exists
	_, _ = database.Exec(`ALTER TABLE chat_history ADD COLUMN is_bot_mention INTEGER DEFAULT 0`)

	_, err = database.Exec(`CREATE TABLE IF NOT EXISTS user_privacy (user_id TEXT PRIMARY KEY, privacy_enabled INTEGER NOT NULL DEFAULT 1)`)
	if err != nil {
		slog.Error("Error while creating user_privacy table", "error", err)
	}

	_, err = database.Exec(`CREATE TABLE IF NOT EXISTS chat_attachments (id INTEGER PRIMARY KEY AUTOINCREMENT, message_id TEXT, attachment_url TEXT, image_description TEXT)`)
	if err != nil {
		slog.Error("Error while creating chat_attachments table", "error", err)
	}
	// idempotent: add columns missing from pre-existing migration-created tables
	_, _ = database.Exec(`ALTER TABLE chat_attachments ADD COLUMN message_id TEXT`)
	_, _ = database.Exec(`ALTER TABLE chat_attachments ADD COLUMN image_description TEXT`)
}

// CloseDB closes the gippity chat history database.
func CloseDB() {
	if database != nil {
		if err := database.Close(); err != nil {
			slog.Error("error closing gippity database", "error", err)
		}
	}
}

func addMessageToDatabase(m *discordgo.MessageCreate, isBotMention bool) {
	stmt, err := database.Prepare("INSERT INTO chat_history (user_id, channel_id, timestamp, message, message_id, guild_id, is_bot_mention) VALUES (?, ?, ?, ?, ?, ?, ?)")
	if err != nil {
		slog.Error("Error while preparing statement", "error", err)
		return
	}
	defer func() { _ = stmt.Close() }()

	botMentionInt := 0
	if isBotMention {
		botMentionInt = 1
	}
	_, err = stmt.Exec(m.Author.ID, m.ChannelID, util.GetTimestampOfMessage(m.ID).Unix(), m.Content, m.ID, m.GuildID, botMentionInt)
	if err != nil {
		slog.Error("Error while inserting message into database", "error", err)
	}
}

func addAttachmentsToDatabase(messageID string, urls []string, description string) {
	stmt, err := database.Prepare("INSERT INTO chat_attachments (message_id, attachment_url, image_description) VALUES (?, ?, ?)")
	if err != nil {
		slog.Error("Error while preparing attachment statement", "error", err)
		return
	}
	defer func() { _ = stmt.Close() }()
	for _, url := range urls {
		if _, err := stmt.Exec(messageID, url, description); err != nil {
			slog.Error("Error while inserting attachment", "error", err)
		}
	}
}

func getLastNMessagesFromDatabase(channelID string, n int) ([]LLMChatMessage, error) {
	stmt, err := database.Prepare(`
	SELECT ch.user_id, ch.channel_id, ch.timestamp, ch.message, ch.message_id, ch.guild_id,
	       COALESCE(ch.is_bot_mention, 0),
	       GROUP_CONCAT(ca.image_description, '|||')
	FROM (
	    SELECT user_id, channel_id, timestamp, message, message_id, guild_id, is_bot_mention
	    FROM chat_history
	    WHERE channel_id = ?
	    ORDER BY timestamp DESC
	    LIMIT ?
	) AS ch
	LEFT JOIN chat_attachments ca ON ca.message_id = ch.message_id
	GROUP BY ch.message_id, ch.user_id, ch.channel_id, ch.timestamp, ch.message, ch.guild_id, ch.is_bot_mention
	ORDER BY ch.timestamp ASC;
	`)
	if err != nil {
		slog.Error("Error while preparing statement", "error", err)
		return nil, err
	}
	defer func() { _ = stmt.Close() }()

	rows, err := stmt.Query(channelID, n)
	if err != nil {
		slog.Error("Error while querying database", "error", err)
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	llmMessages := make([]LLMChatMessage, 0, n)
	for rows.Next() {
		var message LLMChatMessage
		var isBotMentionInt int
		var imageDescConcat *string
		err = rows.Scan(
			&message.UserID,
			&message.ChannelID,
			&message.Timestamp,
			&message.Message,
			&message.MessageID,
			&message.GuildID,
			&isBotMentionInt,
			&imageDescConcat,
		)
		if err != nil {
			slog.Error("Error while scanning row", "error", err)
			return nil, err
		}
		message.IsBotMention = isBotMentionInt != 0
		if imageDescConcat != nil && *imageDescConcat != "" {
			for _, d := range strings.Split(*imageDescConcat, "|||") {
				if d != "" {
					message.ImageDescriptions = append(message.ImageDescriptions, d)
				}
			}
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

// getUserPrivacy returns true (privacy on) by default; explicit opt-out returns false.
func getUserPrivacy(userID string) bool {
	var enabled int
	err := database.QueryRow(`SELECT privacy_enabled FROM user_privacy WHERE user_id = ?`, userID).Scan(&enabled)
	if err == sql.ErrNoRows {
		return true
	}
	if err != nil {
		slog.Error("Error while querying user_privacy", "error", err)
		return true
	}
	return enabled != 0
}

// setUserPrivacy stores or updates the privacy preference for a user.
func setUserPrivacy(userID string, enabled bool) error {
	enabledInt := 0
	if enabled {
		enabledInt = 1
	}
	_, err := database.Exec(
		`INSERT INTO user_privacy (user_id, privacy_enabled) VALUES (?, ?) ON CONFLICT(user_id) DO UPDATE SET privacy_enabled = excluded.privacy_enabled`,
		userID, enabledInt,
	)
	return err
}
