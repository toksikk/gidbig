package gippity

import (
	"errors"
	"log/slog"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/bot"
	"github.com/toksikk/gidbig/internal/shareddb"
	"github.com/toksikk/gidbig/internal/util"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ChatMessage is one stored message of a channel's history.
type ChatMessage struct {
	ID           uint   `gorm:"primaryKey"`
	UserID       string `gorm:"index"`
	ChannelID    string `gorm:"index"`
	Timestamp    int64
	Message      string
	MessageID    string `gorm:"index"`
	GuildID      string `gorm:"index"`
	IsBotMention bool
}

// TableName returns the database table name.
func (ChatMessage) TableName() string { return "gippity_chat_history" }

// UserPrivacy stores a user's history-anonymization preference.
// A missing row means privacy on (the default).
type UserPrivacy struct {
	UserID         string `gorm:"primaryKey"`
	PrivacyEnabled bool   `gorm:"not null"`
}

// TableName returns the database table name.
func (UserPrivacy) TableName() string { return "gippity_user_privacy" }

// ChatAttachment stores the LLM-generated description of a message's images.
type ChatAttachment struct {
	ID               uint   `gorm:"primaryKey"`
	MessageID        string `gorm:"index"`
	AttachmentURL    string
	ImageDescription string
}

// TableName returns the database table name.
func (ChatAttachment) TableName() string { return "gippity_chat_attachments" }

// ChatHistoryEdit stores one version of an edited message.
type ChatHistoryEdit struct {
	ID                uint   `gorm:"primaryKey"`
	OriginalMessageID string `gorm:"index:idx_gippity_chat_history_edits_original,priority:1"`
	EditedContent     string
	Version           int `gorm:"index:idx_gippity_chat_history_edits_original,priority:2"`
	EditedAt          int64
}

// TableName returns the database table name.
func (ChatHistoryEdit) TableName() string { return "gippity_chat_history_edits" }

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
	ReactionSummary   string
}

// openDB uses the shared database injected through Deps, or opens the configured
// gidbig.db when running without a composition root (for example in tests).
func (m *Module) openDB(d bot.Deps) error {
	if d.DB != nil {
		m.db = d.DB
		m.ownsDB = false
		return m.migrateDB()
	}

	db, err := shareddb.Open(shareddb.Filename)
	if err != nil {
		return err
	}
	m.db = db
	m.ownsDB = true
	return m.migrateDB()
}

// migrateDB creates or updates the gippity tables in the shared database.
func (m *Module) migrateDB() error {
	return m.db.AutoMigrate(&ChatMessage{}, &UserPrivacy{}, &ChatAttachment{}, &ChatHistoryEdit{})
}

func (m *Module) addMessageToDatabase(mc *discordgo.MessageCreate, isBotMention bool) {
	message := ChatMessage{
		UserID:       mc.Author.ID,
		ChannelID:    mc.ChannelID,
		Timestamp:    util.GetTimestampOfMessage(mc.ID).Unix(),
		Message:      mc.Content,
		MessageID:    mc.ID,
		GuildID:      mc.GuildID,
		IsBotMention: isBotMention,
	}
	if err := m.db.Create(&message).Error; err != nil {
		slog.Error("Error while inserting message into database", "error", err)
	}
}

func (m *Module) addMessageEditToDatabase(messageID, content string, editedAt int64) {
	m.dbMu.Lock()
	defer m.dbMu.Unlock()

	var maxVersion int
	if err := m.db.Model(&ChatHistoryEdit{}).
		Where("original_message_id = ?", messageID).
		Select("COALESCE(MAX(version), 0)").
		Scan(&maxVersion).Error; err != nil {
		slog.Error("Error while querying max edit version", "error", err)
		return
	}
	edit := ChatHistoryEdit{
		OriginalMessageID: messageID,
		EditedContent:     content,
		Version:           maxVersion + 1,
		EditedAt:          editedAt,
	}
	if err := m.db.Create(&edit).Error; err != nil {
		slog.Error("Error while inserting message edit", "error", err)
	}
}

func (m *Module) addAttachmentsToDatabase(messageID string, urls []string, description string) {
	m.dbMu.Lock()
	defer m.dbMu.Unlock()

	attachment := ChatAttachment{
		MessageID:        messageID,
		AttachmentURL:    strings.Join(urls, ","),
		ImageDescription: description,
	}
	if err := m.db.Create(&attachment).Error; err != nil {
		slog.Error("Error while inserting attachment", "error", err)
	}
}

// chatHistoryRow is the flat row shape returned by the history queries before
// names, timestamps and reaction summaries are filled in.
type chatHistoryRow struct {
	UserID           string
	ChannelID        string
	Timestamp        int64
	Message          string
	MessageID        string
	GuildID          string
	IsBotMention     bool
	ImageDescription *string
}

const lastNMessagesQuery = `
SELECT ch.user_id, ch.channel_id, ch.timestamp,
       COALESCE((SELECT edited_content FROM gippity_chat_history_edits WHERE original_message_id = ch.message_id ORDER BY version DESC LIMIT 1), ch.message) AS message,
       ch.message_id, ch.guild_id,
       COALESCE(ch.is_bot_mention, 0) AS is_bot_mention,
       ca.image_description AS image_description
FROM (
    SELECT user_id, channel_id, timestamp, message, message_id, guild_id, is_bot_mention
    FROM gippity_chat_history
    WHERE channel_id = ?
    ORDER BY timestamp DESC
    LIMIT ?
) AS ch
LEFT JOIN gippity_chat_attachments ca ON ca.message_id = ch.message_id
ORDER BY ch.timestamp ASC;
`

func (m *Module) getLastNMessagesFromDatabase(channelID string, n int) ([]LLMChatMessage, error) {
	var rows []chatHistoryRow
	if err := m.db.Raw(lastNMessagesQuery, channelID, n).Scan(&rows).Error; err != nil {
		slog.Error("Error while querying database", "error", err)
		return nil, err
	}

	llmMessages := make([]LLMChatMessage, 0, len(rows))
	for _, row := range rows {
		message := LLMChatMessage{
			UserID:       row.UserID,
			ChannelID:    row.ChannelID,
			Timestamp:    row.Timestamp,
			Message:      row.Message,
			MessageID:    row.MessageID,
			GuildID:      row.GuildID,
			IsBotMention: row.IsBotMention,
		}
		if row.ImageDescription != nil && *row.ImageDescription != "" {
			message.ImageDescriptions = append(message.ImageDescriptions, *row.ImageDescription)
		}
		m.fillNames(&message)
		llmMessages = append(llmMessages, message)
	}

	return llmMessages, nil
}

// messageByIDRow is the flat row shape for a single stored message.
type messageByIDRow struct {
	UserID          string
	ChannelID       string
	Timestamp       int64
	Message         string
	MessageID       string
	GuildID         string
	IsBotMention    bool
	ImageDescConcat *string
}

func (m *Module) getMessageFromDatabase(messageID string) (*LLMChatMessage, error) {
	var row messageByIDRow
	err := m.db.Table("gippity_chat_history AS ch").
		Select("ch.user_id, ch.channel_id, ch.timestamp, "+
			"COALESCE((SELECT edited_content FROM gippity_chat_history_edits WHERE original_message_id = ch.message_id ORDER BY version DESC LIMIT 1), ch.message) AS message, "+
			"ch.message_id, ch.guild_id, COALESCE(ch.is_bot_mention, 0) AS is_bot_mention, "+
			"GROUP_CONCAT(ca.image_description, '||') AS image_desc_concat").
		Joins("LEFT JOIN gippity_chat_attachments ca ON ca.message_id = ch.message_id").
		Where("ch.message_id = ?", messageID).
		Group("ch.message_id").
		Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	message := LLMChatMessage{
		UserID:       row.UserID,
		ChannelID:    row.ChannelID,
		Timestamp:    row.Timestamp,
		Message:      row.Message,
		MessageID:    row.MessageID,
		GuildID:      row.GuildID,
		IsBotMention: row.IsBotMention,
	}
	if row.ImageDescConcat != nil && *row.ImageDescConcat != "" {
		message.ImageDescriptions = strings.Split(*row.ImageDescConcat, "||")
	}
	m.fillNames(&message)
	return &message, nil
}

// fillNames resolves the cached usernames, channel names and guild names for a
// stored message and derives its human-readable timestamp.
func (m *Module) fillNames(message *LLMChatMessage) {
	message.Username = m.cachedName(message.UserID, func() string {
		return util.GetUsernameForUserIDInGuild(m.session, message.UserID, message.GuildID)
	})
	message.ChannelName = m.cachedName(message.ChannelID, func() string {
		return util.GetChannelName(m.session, message.ChannelID)
	})
	message.GuildName = m.cachedName(message.GuildID, func() string {
		return util.GetGuildName(m.session, message.GuildID)
	})
	message.TimestampString = util.GetTimestampOfMessage(message.MessageID).Format("2006-01-02 15:04:05")
}

// getUserPrivacy returns true (privacy on) by default; explicit opt-out returns false.
func (m *Module) getUserPrivacy(userID string) bool {
	var privacy UserPrivacy
	err := m.db.Where("user_id = ?", userID).First(&privacy).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return true
	}
	if err != nil {
		slog.Error("Error while querying user_privacy", "error", err)
		return true
	}
	return privacy.PrivacyEnabled
}

// setUserPrivacy stores or updates the privacy preference for a user.
func (m *Module) setUserPrivacy(userID string, enabled bool) error {
	privacy := UserPrivacy{UserID: userID, PrivacyEnabled: enabled}
	return m.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"privacy_enabled"}),
	}).Create(&privacy).Error
}
