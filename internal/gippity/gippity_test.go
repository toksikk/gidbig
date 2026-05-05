package gippity

import (
	"database/sql"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
)

func setupGippityTest(t *testing.T) *discordgo.Session {
	t.Helper()

	previousDatabase := database
	previousDiscordSession := discordSession
	previousAllowedGuildIDs := allowedGuildIDs
	previousIgnoredUserIDs := ignoredUserIDs
	previousUserMessageCount := userMessageCount
	previousUserMessageCountLastReset := userMessageCountLastReset
	previousGenerateAnswerFunc := generateAnswerFunc

	testDB, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	if _, err := testDB.Exec(`CREATE TABLE chat_history (user_id text, channel_id text, timestamp integer, message text, message_id text, guild_id text, is_bot_mention integer default 0)`); err != nil {
		t.Fatalf("create chat_history table: %v", err)
	}
	if _, err := testDB.Exec(`CREATE TABLE user_privacy (user_id TEXT PRIMARY KEY, privacy_enabled INTEGER NOT NULL DEFAULT 1)`); err != nil {
		t.Fatalf("create user_privacy table: %v", err)
	}

	state := discordgo.NewState()
	state.User = &discordgo.User{ID: "bot-user"}
	session := &discordgo.Session{State: state}

	database = testDB
	discordSession = session
	allowedGuildIDs = map[string]bool{"allowed-guild": true}
	ignoredUserIDs = map[string]bool{}
	userMessageCount = map[string]int{}
	userMessageCountLastReset = map[string]time.Time{}
	generateAnswerFunc = generateAnswer

	t.Cleanup(func() {
		_ = testDB.Close()
		database = previousDatabase
		discordSession = previousDiscordSession
		allowedGuildIDs = previousAllowedGuildIDs
		ignoredUserIDs = previousIgnoredUserIDs
		userMessageCount = previousUserMessageCount
		userMessageCountLastReset = previousUserMessageCountLastReset
		generateAnswerFunc = previousGenerateAnswerFunc
	})

	return session
}

func gippityTestMessage(content string, mentions ...*discordgo.User) *discordgo.MessageCreate {
	return &discordgo.MessageCreate{
		Message: &discordgo.Message{
			ID:        "1367540149316120650",
			ChannelID: "channel-1",
			GuildID:   "allowed-guild",
			Content:   content,
			Author: &discordgo.User{
				ID:       "user-1",
				Username: "Alice",
			},
			Mentions: mentions,
		},
	}
}

func TestOnMessageCreate_StoresNonMentionedMessageWithoutGeneratingAnswer(t *testing.T) {
	session := setupGippityTest(t)
	generateAnswerFunc = func(_ *discordgo.MessageCreate, _ []string) (string, error) {
		t.Fatal("generateAnswerFunc should not be called for non-mentioned messages")
		return "", nil
	}

	onMessageCreate(session, gippityTestMessage("ordinary channel message"))

	var message string
	err := database.QueryRow("SELECT message FROM chat_history WHERE message_id = ?", "1367540149316120650").Scan(&message)
	if err != nil {
		t.Fatalf("expected non-mentioned message to be stored: %v", err)
	}
	if message != "ordinary channel message" {
		t.Errorf("stored message = %q, want %q", message, "ordinary channel message")
	}
	if len(userMessageCount) != 0 {
		t.Errorf("non-mentioned message should not count toward mention rate limit, got %d users", len(userMessageCount))
	}
}

func TestLimited_AllowsMentionedMessageInAllowedGuild(t *testing.T) {
	setupGippityTest(t)

	m := gippityTestMessage("hey <@bot-user>", &discordgo.User{ID: "bot-user"})

	if limited(m) {
		t.Fatal("mentioned message in allowed guild should not be limited")
	}
}

func TestLimited_BlocksNonMentionedMessageInAllowedGuild(t *testing.T) {
	setupGippityTest(t)

	if !limited(gippityTestMessage("ordinary channel message")) {
		t.Fatal("non-mentioned message in allowed guild should be limited")
	}
}

func TestOnMessageCreate_StoresBotMentionFlag(t *testing.T) {
	session := setupGippityTest(t)
	generateAnswerFunc = func(_ *discordgo.MessageCreate, _ []string) (string, error) {
		return "", nil
	}

	botUser := &discordgo.User{ID: "bot-user"}
	m := gippityTestMessage("hey <@bot-user>", botUser)
	m.Message.ID = "mention-msg-id"

	onMessageCreate(session, m)

	var isBotMention int
	err := database.QueryRow("SELECT is_bot_mention FROM chat_history WHERE message_id = ?", "mention-msg-id").Scan(&isBotMention)
	if err != nil {
		t.Fatalf("expected message to be stored: %v", err)
	}
	if isBotMention != 1 {
		t.Errorf("is_bot_mention = %d, want 1", isBotMention)
	}
}

func TestOnMessageCreate_StoresNonMentionWithZeroFlag(t *testing.T) {
	session := setupGippityTest(t)
	generateAnswerFunc = func(_ *discordgo.MessageCreate, _ []string) (string, error) {
		return "", nil
	}

	m := gippityTestMessage("just chatting")
	m.Message.ID = "non-mention-msg-id"

	onMessageCreate(session, m)

	var isBotMention int
	err := database.QueryRow("SELECT is_bot_mention FROM chat_history WHERE message_id = ?", "non-mention-msg-id").Scan(&isBotMention)
	if err != nil {
		t.Fatalf("expected message to be stored: %v", err)
	}
	if isBotMention != 0 {
		t.Errorf("is_bot_mention = %d, want 0", isBotMention)
	}
}

func TestGetUserPrivacy_DefaultsToTrue(t *testing.T) {
	setupGippityTest(t)

	if !getUserPrivacy("unknown-user") {
		t.Error("getUserPrivacy for unknown user should default to true (privacy on)")
	}
}

func TestSetAndGetUserPrivacy(t *testing.T) {
	setupGippityTest(t)

	if err := setUserPrivacy("user-a", false); err != nil {
		t.Fatalf("setUserPrivacy: %v", err)
	}
	if getUserPrivacy("user-a") {
		t.Error("getUserPrivacy should return false after setting privacy off")
	}

	if err := setUserPrivacy("user-a", true); err != nil {
		t.Fatalf("setUserPrivacy: %v", err)
	}
	if !getUserPrivacy("user-a") {
		t.Error("getUserPrivacy should return true after re-enabling privacy")
	}
}
