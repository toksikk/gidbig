package gippity

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	openai "github.com/openai/openai-go/v3"
)

var previousDescribeImagesFunc func([]string) (string, error)

func setupGippityTest(t *testing.T) *discordgo.Session {
	t.Helper()

	previousDatabase := database
	previousDiscordSession := discordSession
	previousAllowedGuildIDs := allowedGuildIDs
	previousIgnoredUserIDs := ignoredUserIDs
	previousUserMessageCount := userMessageCount
	previousUserMessageCountLastReset := userMessageCountLastReset
	previousGenerateAnswerFunc := generateAnswerFunc
	previousDescribeImagesFunc = describeImagesFunc
	previousChatCompletionFunc := chatCompletionFunc
	previousChannelTypingFunc := channelTypingFunc

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
	if _, err := testDB.Exec(`CREATE TABLE chat_attachments (id INTEGER PRIMARY KEY AUTOINCREMENT, message_id TEXT, attachment_url TEXT, image_description TEXT)`); err != nil {
		t.Fatalf("create chat_attachments table: %v", err)
	}
	if _, err := testDB.Exec(`CREATE TABLE chat_history_edits (id INTEGER PRIMARY KEY AUTOINCREMENT, original_message_id TEXT, edited_content TEXT, version INTEGER, edited_at INTEGER)`); err != nil {
		t.Fatalf("create chat_history_edits table: %v", err)
	}

	session, err := discordgo.New("")
	if err != nil {
		t.Fatalf("discordgo.New: %v", err)
	}
	session.State.User = &discordgo.User{ID: "bot-user"}

	database = testDB
	discordSession = session
	allowedGuildIDs = map[string]bool{"allowed-guild": true}
	ignoredUserIDs = map[string]bool{}
	userMessageCount = map[string]int{}
	userMessageCountLastReset = map[string]time.Time{}
	generateAnswerFunc = generateAnswer
	channelTypingFunc = func(_ *discordgo.Session, _ string) {}

	t.Cleanup(func() {
		_ = testDB.Close()
		database = previousDatabase
		discordSession = previousDiscordSession
		allowedGuildIDs = previousAllowedGuildIDs
		ignoredUserIDs = previousIgnoredUserIDs
		userMessageCount = previousUserMessageCount
		userMessageCountLastReset = previousUserMessageCountLastReset
		generateAnswerFunc = previousGenerateAnswerFunc
		describeImagesFunc = previousDescribeImagesFunc
		chatCompletionFunc = previousChatCompletionFunc
		channelTypingFunc = previousChannelTypingFunc
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
	m.ID = "mention-msg-id"

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
	m.ID = "non-mention-msg-id"

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

func gippityTestMessageWithImage(content string, imageURL string) *discordgo.MessageCreate {
	return &discordgo.MessageCreate{
		Message: &discordgo.Message{
			ID:        "img-msg-id",
			ChannelID: "channel-1",
			GuildID:   "allowed-guild",
			Content:   content,
			Author: &discordgo.User{
				ID:       "user-1",
				Username: "Alice",
			},
			Attachments: []*discordgo.MessageAttachment{
				{
					ID:       "att-1",
					URL:      imageURL,
					Filename: "photo.png",
				},
			},
		},
	}
}

func TestOnMessageCreate_DescribesImageForNonMentionMessage(t *testing.T) {
	session := setupGippityTest(t)
	generateAnswerFunc = func(_ *discordgo.MessageCreate, _ []string) (string, error) {
		t.Fatal("generateAnswerFunc should not be called for non-mention message")
		return "", nil
	}
	describeCalled := false
	describeImagesFunc = func(urls []string) (string, error) {
		describeCalled = true
		if len(urls) != 1 || urls[0] != "https://cdn.example.com/photo.png" {
			t.Errorf("unexpected image URLs: %v", urls)
		}
		return "a cat on a mat", nil
	}

	m := gippityTestMessageWithImage("check this out", "https://cdn.example.com/photo.png")

	onMessageCreate(session, m)

	if !describeCalled {
		t.Error("describeImagesFunc should have been called for image attachment")
	}

	var url, desc string
	err := database.QueryRow("SELECT attachment_url, image_description FROM chat_attachments WHERE message_id = ?", "img-msg-id").Scan(&url, &desc)
	if err != nil {
		t.Fatalf("expected attachment row to exist: %v", err)
	}
	if url != "https://cdn.example.com/photo.png" {
		t.Errorf("stored url = %q, want %q", url, "https://cdn.example.com/photo.png")
	}
	if desc != "a cat on a mat" {
		t.Errorf("stored description = %q, want %q", desc, "a cat on a mat")
	}
}

func TestGetMessageFromDatabase_Found(t *testing.T) {
	setupGippityTest(t)
	idToNameCache["user-1"] = "Alice"
	idToNameCache["channel-1"] = "general"
	idToNameCache["allowed-guild"] = "Test Guild"

	if _, err := database.Exec(`INSERT INTO chat_history (user_id, channel_id, timestamp, message, message_id, guild_id, is_bot_mention) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"user-1", "channel-1", 1000, "hello from db", "db-msg-id", "allowed-guild", 0); err != nil {
		t.Fatalf("insert: %v", err)
	}

	msg, err := getMessageFromDatabase("db-msg-id")
	if err != nil {
		t.Fatalf("getMessageFromDatabase: %v", err)
	}
	if msg == nil {
		t.Fatal("expected non-nil message")
	}
	if msg.Message != "hello from db" {
		t.Errorf("Message = %q, want %q", msg.Message, "hello from db")
	}
	if msg.UserID != "user-1" {
		t.Errorf("UserID = %q, want %q", msg.UserID, "user-1")
	}
}

func TestGetMessageFromDatabase_NotFound(t *testing.T) {
	setupGippityTest(t)

	msg, err := getMessageFromDatabase("nonexistent-msg-id")
	if err != nil {
		t.Fatalf("getMessageFromDatabase returned unexpected error: %v", err)
	}
	if msg != nil {
		t.Errorf("expected nil message for nonexistent ID, got %+v", msg)
	}
}


func TestGenerateAnswer_NoReference_NoSystemNoteInjected(t *testing.T) {
	setupGippityTest(t)

	fetchCalled := false
	prev := fetchReferencedMessageFunc
	t.Cleanup(func() { fetchReferencedMessageFunc = prev })
	fetchReferencedMessageFunc = func(_ *discordgo.Session, _ *discordgo.MessageReference) (*discordgo.Message, error) {
		fetchCalled = true
		return nil, nil
	}

	var capturedMessages []openai.ChatCompletionMessageParamUnion
	chatCompletionFunc = func(_ context.Context, params openai.ChatCompletionNewParams) (*openai.ChatCompletion, error) {
		capturedMessages = params.Messages
		return &openai.ChatCompletion{
			Choices: []openai.ChatCompletionChoice{{Message: openai.ChatCompletionMessage{Content: "ok"}}},
		}, nil
	}

	m := &discordgo.MessageCreate{
		Message: &discordgo.Message{
			ID:        "msg-no-ref",
			ChannelID: "channel-1",
			GuildID:   "allowed-guild",
			Content:   "hello <@bot-user>",
			Author:    &discordgo.User{ID: "user-1", Username: "Alice"},
			Mentions:  []*discordgo.User{{ID: "bot-user"}},
		},
	}

	if _, err := generateAnswer(m, nil); err != nil {
		t.Fatalf("generateAnswer: %v", err)
	}

	if fetchCalled {
		t.Error("fetchReferencedMessageFunc must not be called when MessageReference is nil")
	}
	for _, msg := range capturedMessages {
		if msg.OfSystem != nil {
			c := msg.OfSystem.Content.OfString.Value
			if strings.Contains(c, "[System note:") {
				t.Errorf("unexpected system note injected when no MessageReference: %q", c)
			}
		}
	}
}

func TestGenerateAnswer_ReferencedMessageOptedOutAuthor_PlaceholderInjected(t *testing.T) {
	setupGippityTest(t)

	if !getUserPrivacy("opted-out-user") {
		t.Fatal("test precondition: opted-out-user should have privacy ON by default")
	}

	prev := fetchReferencedMessageFunc
	t.Cleanup(func() { fetchReferencedMessageFunc = prev })
	fetchReferencedMessageFunc = func(_ *discordgo.Session, _ *discordgo.MessageReference) (*discordgo.Message, error) {
		return &discordgo.Message{
			ID:      "ref-id",
			Content: "secret content",
			Author:  &discordgo.User{ID: "opted-out-user", Username: "Bob", Bot: false},
		}, nil
	}

	var capturedMessages []openai.ChatCompletionMessageParamUnion
	chatCompletionFunc = func(_ context.Context, params openai.ChatCompletionNewParams) (*openai.ChatCompletion, error) {
		capturedMessages = params.Messages
		return &openai.ChatCompletion{
			Choices: []openai.ChatCompletionChoice{{Message: openai.ChatCompletionMessage{Content: "ok"}}},
		}, nil
	}

	m := &discordgo.MessageCreate{
		Message: &discordgo.Message{
			ID:        "msg-with-ref",
			ChannelID: "channel-1",
			GuildID:   "allowed-guild",
			Content:   "what do you think? <@bot-user>",
			Author:    &discordgo.User{ID: "user-1", Username: "Alice"},
			Mentions:  []*discordgo.User{{ID: "bot-user"}},
			MessageReference: &discordgo.MessageReference{
				MessageID: "ref-id",
				ChannelID: "channel-1",
				GuildID:   "allowed-guild",
			},
		},
	}

	if _, err := generateAnswer(m, nil); err != nil {
		t.Fatalf("generateAnswer: %v", err)
	}

	found := false
	for _, msg := range capturedMessages {
		if msg.OfSystem != nil {
			c := msg.OfSystem.Content.OfString.Value
			if strings.Contains(c, "[System note:") && strings.Contains(c, "[message content hidden -- user opted out]") {
				found = true
				break
			}
		}
	}
	if !found {
		t.Error("expected system note with privacy placeholder in messages passed to LLM")
	}
}

func TestGenerateAnswer_ReferencedMessageOptInAuthor_ContentInjected(t *testing.T) {
	setupGippityTest(t)

	if err := setUserPrivacy("optin-user", false); err != nil {
		t.Fatalf("setUserPrivacy: %v", err)
	}

	prev := fetchReferencedMessageFunc
	t.Cleanup(func() { fetchReferencedMessageFunc = prev })
	fetchReferencedMessageFunc = func(_ *discordgo.Session, _ *discordgo.MessageReference) (*discordgo.Message, error) {
		return &discordgo.Message{
			ID:      "ref-id",
			Content: "visible content",
			Author:  &discordgo.User{ID: "optin-user", Username: "Carol", Bot: false},
		}, nil
	}

	var capturedMessages []openai.ChatCompletionMessageParamUnion
	chatCompletionFunc = func(_ context.Context, params openai.ChatCompletionNewParams) (*openai.ChatCompletion, error) {
		capturedMessages = params.Messages
		return &openai.ChatCompletion{
			Choices: []openai.ChatCompletionChoice{{Message: openai.ChatCompletionMessage{Content: "ok"}}},
		}, nil
	}

	m := &discordgo.MessageCreate{
		Message: &discordgo.Message{
			ID:        "msg-with-ref-optin",
			ChannelID: "channel-1",
			GuildID:   "allowed-guild",
			Content:   "what do you think? <@bot-user>",
			Author:    &discordgo.User{ID: "user-1", Username: "Alice"},
			Mentions:  []*discordgo.User{{ID: "bot-user"}},
			MessageReference: &discordgo.MessageReference{
				MessageID: "ref-id",
				ChannelID: "channel-1",
				GuildID:   "allowed-guild",
			},
		},
	}

	if _, err := generateAnswer(m, nil); err != nil {
		t.Fatalf("generateAnswer: %v", err)
	}

	found := false
	for _, msg := range capturedMessages {
		if msg.OfSystem != nil {
			c := msg.OfSystem.Content.OfString.Value
			if strings.Contains(c, "[System note:") && strings.Contains(c, "visible content") {
				found = true
				break
			}
		}
	}
	if !found {
		t.Error("expected system note with visible content in messages passed to LLM")
	}
}

func gippityTestMessageUpdate(msgID, content, guildID, authorID string) *discordgo.MessageUpdate {
	return &discordgo.MessageUpdate{
		Message: &discordgo.Message{
			ID:        msgID,
			ChannelID: "channel-1",
			GuildID:   guildID,
			Content:   content,
			Author:    &discordgo.User{ID: authorID, Username: "Alice"},
		},
	}
}

func TestOnMessageUpdate_PrivacyEnabledUser_EditNotPersisted(t *testing.T) {
	session := setupGippityTest(t)

	if _, err := database.Exec(`INSERT INTO chat_history (user_id, channel_id, timestamp, message, message_id, guild_id, is_bot_mention) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"user-1", "channel-1", 1000, "original", "edit-msg-id", "allowed-guild", 0); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// privacy on by default for user-1
	onMessageUpdate(session, gippityTestMessageUpdate("edit-msg-id", "edited content", "allowed-guild", "user-1"))

	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM chat_history_edits WHERE original_message_id = ?`, "edit-msg-id").Scan(&count); err != nil {
		t.Fatalf("query: %v", err)
	}
	if count != 0 {
		t.Errorf("edit should not be persisted for privacy-on user, got %d row(s)", count)
	}
}

func TestOnMessageUpdate_PrivacyDisabledUser_EditPersisted(t *testing.T) {
	session := setupGippityTest(t)

	if err := setUserPrivacy("user-1", false); err != nil {
		t.Fatalf("setUserPrivacy: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO chat_history (user_id, channel_id, timestamp, message, message_id, guild_id, is_bot_mention) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"user-1", "channel-1", 1000, "original", "edit-msg-id2", "allowed-guild", 0); err != nil {
		t.Fatalf("insert: %v", err)
	}

	onMessageUpdate(session, gippityTestMessageUpdate("edit-msg-id2", "first edit", "allowed-guild", "user-1"))
	onMessageUpdate(session, gippityTestMessageUpdate("edit-msg-id2", "second edit", "allowed-guild", "user-1"))

	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM chat_history_edits WHERE original_message_id = ?`, "edit-msg-id2").Scan(&count); err != nil {
		t.Fatalf("query: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 edit rows, got %d", count)
	}

	var version int
	var content string
	if err := database.QueryRow(`SELECT version, edited_content FROM chat_history_edits WHERE original_message_id = ? ORDER BY version DESC LIMIT 1`, "edit-msg-id2").Scan(&version, &content); err != nil {
		t.Fatalf("query latest edit: %v", err)
	}
	if version != 2 {
		t.Errorf("latest version = %d, want 2", version)
	}
	if content != "second edit" {
		t.Errorf("latest content = %q, want %q", content, "second edit")
	}
}

func TestOnMessageUpdate_NilAuthor_Skipped(t *testing.T) {
	session := setupGippityTest(t)

	m := &discordgo.MessageUpdate{
		Message: &discordgo.Message{
			ID:        "nil-author-msg",
			ChannelID: "channel-1",
			GuildID:   "allowed-guild",
			Content:   "embed update",
			Author:    nil,
		},
	}
	onMessageUpdate(session, m)

	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM chat_history_edits WHERE original_message_id = ?`, "nil-author-msg").Scan(&count); err != nil {
		t.Fatalf("query: %v", err)
	}
	if count != 0 {
		t.Errorf("nil-author update should not be persisted, got %d row(s)", count)
	}
}

func TestOnMessageUpdate_MessageNotInHistory_Skipped(t *testing.T) {
	session := setupGippityTest(t)

	if err := setUserPrivacy("user-1", false); err != nil {
		t.Fatalf("setUserPrivacy: %v", err)
	}

	onMessageUpdate(session, gippityTestMessageUpdate("unknown-msg-id", "edit of unknown", "allowed-guild", "user-1"))

	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM chat_history_edits WHERE original_message_id = ?`, "unknown-msg-id").Scan(&count); err != nil {
		t.Fatalf("query: %v", err)
	}
	if count != 0 {
		t.Errorf("edit of message not in chat_history should not be persisted, got %d row(s)", count)
	}
}

func TestGetLastNMessagesFromDatabase_ShowsLatestEdit(t *testing.T) {
	setupGippityTest(t)
	idToNameCache["user-1"] = "Alice"
	idToNameCache["channel-1"] = "general"
	idToNameCache["allowed-guild"] = "Test Guild"

	if _, err := database.Exec(`INSERT INTO chat_history (user_id, channel_id, timestamp, message, message_id, guild_id, is_bot_mention) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"user-1", "channel-1", 1000, "original message", "edited-hist-msg", "allowed-guild", 0); err != nil {
		t.Fatalf("insert chat_history: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO chat_history_edits (original_message_id, edited_content, version, edited_at) VALUES (?, ?, ?, ?)`,
		"edited-hist-msg", "v1 edit", 1, 1001); err != nil {
		t.Fatalf("insert edit v1: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO chat_history_edits (original_message_id, edited_content, version, edited_at) VALUES (?, ?, ?, ?)`,
		"edited-hist-msg", "v2 edit", 2, 1002); err != nil {
		t.Fatalf("insert edit v2: %v", err)
	}

	msgs, err := getLastNMessagesFromDatabase("channel-1", 10)
	if err != nil {
		t.Fatalf("getLastNMessagesFromDatabase: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Message != "v2 edit" {
		t.Errorf("message content = %q, want %q", msgs[0].Message, "v2 edit")
	}
}

func TestGetLastNMessagesFromDatabase_IncludesImageDescriptions(t *testing.T) {
	setupGippityTest(t)
	idToNameCache["user-1"] = "Alice"
	idToNameCache["channel-1"] = "general"
	idToNameCache["allowed-guild"] = "Test Guild"

	if _, err := database.Exec(`INSERT INTO chat_history (user_id, channel_id, timestamp, message, message_id, guild_id, is_bot_mention) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"user-1", "channel-1", 1000, "look at this", "msg-with-img", "allowed-guild", 0); err != nil {
		t.Fatalf("insert chat_history: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO chat_attachments (message_id, attachment_url, image_description) VALUES (?, ?, ?)`,
		"msg-with-img", "https://cdn.example.com/photo.png", "a fluffy dog"); err != nil {
		t.Fatalf("insert chat_attachments: %v", err)
	}

	msgs, err := getLastNMessagesFromDatabase("channel-1", 10)
	if err != nil {
		t.Fatalf("getLastNMessagesFromDatabase: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if len(msgs[0].ImageDescriptions) != 1 {
		t.Fatalf("expected 1 image description, got %d", len(msgs[0].ImageDescriptions))
	}
	if msgs[0].ImageDescriptions[0] != "a fluffy dog" {
		t.Errorf("image description = %q, want %q", msgs[0].ImageDescriptions[0], "a fluffy dog")
	}
}
