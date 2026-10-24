package gippity

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	openai "github.com/openai/openai-go/v3"
	"github.com/toksikk/gidbig/internal/llm"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// execSQL runs a statement through GORM, returning RowsAffected so tests keep
// the familiar `if _, err := ...` shape.
func execSQL(t *testing.T, db *gorm.DB, query string, args ...any) (int64, error) {
	t.Helper()
	result := db.Exec(query, args...)
	return result.RowsAffected, result.Error
}

// queryRow returns a single row through GORM's Raw interface.
func queryRow(t *testing.T, db *gorm.DB, query string, args ...any) *sql.Row {
	t.Helper()
	return db.Raw(query, args...).Row()
}

func setupGippityTest(t *testing.T) (*Module, *discordgo.Session) {
	t.Helper()

	testDB, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "gippity.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	if err := testDB.AutoMigrate(&ChatMessage{}, &UserPrivacy{}, &UserConsent{}, &ChatAttachment{}, &ChatHistoryEdit{}); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}

	session, err := discordgo.New("")
	if err != nil {
		t.Fatalf("discordgo.New: %v", err)
	}
	session.State.User = &discordgo.User{ID: "bot-user"}

	m := New()
	t.Setenv("OPENAI_API_KEY", "test-key")
	llmClient, err := llm.New(llm.Options{})
	if err != nil {
		t.Fatalf("llm.New: %v", err)
	}
	m.llmClient = llmClient
	m.db = testDB
	m.session = session
	m.allowedGuildIDs = map[string]bool{"allowed-guild": true}
	m.ignoredUserIDs = map[string]bool{}
	m.userMessageCount = map[string]int{}
	m.userMessageCountLastReset = map[string]time.Time{}
	m.generateAnswerFunc = m.generateAnswer
	m.channelTypingFunc = func(_ *discordgo.Session, _ string) {}
	m.fetchMessageReactionsFunc = func(_ *discordgo.Session, _, _ string) (*discordgo.Message, error) {
		return &discordgo.Message{}, nil
	}
	m.messageSendFunc = func(_ *discordgo.Session, _, _ string) (*discordgo.Message, error) {
		return &discordgo.Message{}, nil
	}
	m.messageThreadStartFunc = func(_ *discordgo.Session, _, _ string, _ *discordgo.ThreadStart) (*discordgo.Channel, error) {
		return &discordgo.Channel{ID: "thread-1"}, nil
	}
	m.channelIsThreadFunc = func(_ *discordgo.Session, _ string) bool { return false }

	t.Cleanup(func() {
		if sqlDB, err := testDB.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})

	return m, session
}

func TestIsLimitedUserUsesConfiguredLimit(t *testing.T) {
	m, _ := setupGippityTest(t)
	m.userMessageLimit = 2
	message := &discordgo.MessageCreate{Message: &discordgo.Message{Author: &discordgo.User{ID: "user"}}}

	if m.isLimitedUser(message) {
		t.Fatal("first message was limited")
	}
	if m.isLimitedUser(message) {
		t.Fatal("second message was limited")
	}
	if !m.isLimitedUser(message) {
		t.Fatal("third message was not limited")
	}
}

func TestStartTypingKeepAliveRefreshesUntilStopped(t *testing.T) {
	m, _ := setupGippityTest(t)
	old := typingKeepAliveInterval
	typingKeepAliveInterval = 5 * time.Millisecond
	t.Cleanup(func() { typingKeepAliveInterval = old })

	var mu sync.Mutex
	var calls int
	m.channelTypingFunc = func(_ *discordgo.Session, _ string) {
		mu.Lock()
		calls++
		mu.Unlock()
	}

	stop := m.startTypingKeepAlive("channel-1")

	deadline := time.Now().Add(time.Second)
	for {
		mu.Lock()
		n := calls
		mu.Unlock()
		if n >= 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("typing indicator did not refresh, calls = %d", n)
		}
		time.Sleep(time.Millisecond)
	}

	stop()
	stop() // idempotent

	mu.Lock()
	before := calls
	mu.Unlock()
	time.Sleep(30 * time.Millisecond)
	mu.Lock()
	after := calls
	mu.Unlock()
	if after != before {
		t.Fatalf("typing indicator kept refreshing after stop: %d -> %d", before, after)
	}
}

func TestDescribeImagesUsesConfiguredVisionModel(t *testing.T) {
	m, _ := setupGippityTest(t)
	var model string
	m.visionCompletionFunc = func(_ context.Context, params openai.ChatCompletionNewParams) (*openai.ChatCompletion, error) {
		model = params.Model
		return &openai.ChatCompletion{
			Choices: []openai.ChatCompletionChoice{{Message: openai.ChatCompletionMessage{Content: "description"}}},
		}, nil
	}

	got, err := m.describeImages([]string{"https://example.com/image.png"})
	if err != nil {
		t.Fatalf("describeImages: %v", err)
	}
	if got != "description" {
		t.Errorf("describeImages = %q", got)
	}
	if model != m.llmClient.VisionModel() {
		t.Errorf("model = %q, want configured vision model %q", model, m.llmClient.VisionModel())
	}
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

func TestOnMessageCreate_NilAuthor_NoPanic(t *testing.T) {
	m, session := setupGippityTest(t)
	m.generateAnswerFunc = func(_ *discordgo.MessageCreate, _ []string) (string, error) {
		t.Fatal("generateAnswerFunc should not be called for malformed events")
		return "", nil
	}

	noAuthor := gippityTestMessage("no author")
	noAuthor.Author = nil
	for _, mc := range []*discordgo.MessageCreate{nil, {}, noAuthor} {
		m.onMessageCreate(session, mc)
	}

	var count int
	if err := queryRow(t, m.db, "SELECT COUNT(*) FROM gippity_chat_history").Scan(&count); err != nil {
		t.Fatalf("count history: %v", err)
	}
	if count != 0 {
		t.Errorf("stored %d malformed messages, want 0", count)
	}
}

func TestIsMentioned_NilBotUser(t *testing.T) {
	m, session := setupGippityTest(t)
	session.State.User = nil
	if m.isMentioned(gippityTestMessage("hi", &discordgo.User{ID: "bot-user"})) {
		t.Error("isMentioned = true without a bot user, want false")
	}
}

func TestOnMessageCreate_StoresNonMentionedMessageWithoutGeneratingAnswer(t *testing.T) {
	m, session := setupGippityTest(t)
	m.generateAnswerFunc = func(_ *discordgo.MessageCreate, _ []string) (string, error) {
		t.Fatal("generateAnswerFunc should not be called for non-mentioned messages")
		return "", nil
	}

	m.onMessageCreate(session, gippityTestMessage("ordinary channel message"))

	var message string
	err := queryRow(t, m.db, "SELECT message FROM gippity_chat_history WHERE message_id = ?", "1367540149316120650").Scan(&message)
	if err != nil {
		t.Fatalf("expected non-mentioned message to be stored: %v", err)
	}
	if message != "ordinary channel message" {
		t.Errorf("stored message = %q, want %q", message, "ordinary channel message")
	}
	if len(m.userMessageCount) != 0 {
		t.Errorf("non-mentioned message should not count toward mention rate limit, got %d users", len(m.userMessageCount))
	}
}

func TestEligible_AllowsOrdinaryMessageInAllowedGuild(t *testing.T) {
	m, _ := setupGippityTest(t)

	msg := gippityTestMessage("hey <@bot-user>", &discordgo.User{ID: "bot-user"})

	if !m.eligible(msg) {
		t.Fatal("ordinary message in allowed guild should be eligible")
	}
}

func TestEligible_RejectsBotAuthor(t *testing.T) {
	m, _ := setupGippityTest(t)

	msg := gippityTestMessage("beep boop")
	msg.Author.Bot = true

	if m.eligible(msg) {
		t.Fatal("bot author should not be eligible")
	}
}

func TestEligible_RejectsNilAuthor(t *testing.T) {
	m, _ := setupGippityTest(t)

	msg := gippityTestMessage("no author")
	msg.Author = nil

	if m.eligible(msg) {
		t.Fatal("nil author should not be eligible")
	}
}

func TestEligible_RejectsIgnoredUser(t *testing.T) {
	m, _ := setupGippityTest(t)
	m.ignoredUserIDs["user-1"] = true

	if m.eligible(gippityTestMessage("ignored")) {
		t.Fatal("ignored user should not be eligible")
	}
}

func TestEligible_RejectsDisallowedGuild(t *testing.T) {
	m, _ := setupGippityTest(t)

	msg := gippityTestMessage("other guild")
	msg.GuildID = "other-guild"

	if m.eligible(msg) {
		t.Fatal("disallowed guild should not be eligible")
	}
}

func TestEligible_RejectsUnsupportedMessageType(t *testing.T) {
	m, _ := setupGippityTest(t)

	msg := gippityTestMessage("joined the server")
	msg.Type = discordgo.MessageTypeGuildMemberJoin

	if m.eligible(msg) {
		t.Fatal("system message type should not be eligible")
	}
}

func TestOnMessageCreate_StoresBotMentionFlag(t *testing.T) {
	m, session := setupGippityTest(t)
	m.generateAnswerFunc = func(_ *discordgo.MessageCreate, _ []string) (string, error) {
		return "", nil
	}

	botUser := &discordgo.User{ID: "bot-user"}
	msg := gippityTestMessage("hey <@bot-user>", botUser)
	msg.ID = "mention-msg-id"

	m.onMessageCreate(session, msg)

	var isBotMention int
	err := queryRow(t, m.db, "SELECT is_bot_mention FROM gippity_chat_history WHERE message_id = ?", "mention-msg-id").Scan(&isBotMention)
	if err != nil {
		t.Fatalf("expected message to be stored: %v", err)
	}
	if isBotMention != 1 {
		t.Errorf("is_bot_mention = %d, want 1", isBotMention)
	}
}

func TestOnMessageCreate_StoresNonMentionWithZeroFlag(t *testing.T) {
	m, session := setupGippityTest(t)
	m.generateAnswerFunc = func(_ *discordgo.MessageCreate, _ []string) (string, error) {
		return "", nil
	}

	msg := gippityTestMessage("just chatting")
	msg.ID = "non-mention-msg-id"

	m.onMessageCreate(session, msg)

	var isBotMention int
	err := queryRow(t, m.db, "SELECT is_bot_mention FROM gippity_chat_history WHERE message_id = ?", "non-mention-msg-id").Scan(&isBotMention)
	if err != nil {
		t.Fatalf("expected message to be stored: %v", err)
	}
	if isBotMention != 0 {
		t.Errorf("is_bot_mention = %d, want 0", isBotMention)
	}
}

func TestGetUserPrivacy_DefaultsToTrue(t *testing.T) {
	m, _ := setupGippityTest(t)

	if !m.getUserPrivacy("unknown-user") {
		t.Error("getUserPrivacy for unknown user should default to true (privacy on)")
	}
}

func TestSetAndGetUserPrivacy(t *testing.T) {
	m, _ := setupGippityTest(t)

	if err := m.setUserPrivacy("user-a", false); err != nil {
		t.Fatalf("setUserPrivacy: %v", err)
	}
	if m.getUserPrivacy("user-a") {
		t.Error("getUserPrivacy should return false after setting privacy off")
	}

	if err := m.setUserPrivacy("user-a", true); err != nil {
		t.Fatalf("setUserPrivacy: %v", err)
	}
	if !m.getUserPrivacy("user-a") {
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

func TestOnMessageCreate_SkipsVisionWhenNonMentionNotAnswered(t *testing.T) {
	m, session := setupGippityTest(t)
	m.generateAnswerFunc = func(_ *discordgo.MessageCreate, _ []string) (string, error) {
		t.Fatal("generateAnswerFunc should not be called for non-mention message")
		return "", nil
	}
	m.describeImagesFunc = func(_ []string) (string, error) {
		t.Fatal("describeImagesFunc should not be called before a positive response decision")
		return "", nil
	}

	msg := gippityTestMessageWithImage("check this out", "https://cdn.example.com/photo.png")

	m.onMessageCreate(session, msg)
}

func TestOnMessageCreate_DescribesImageForEligibleNonMentionMessage(t *testing.T) {
	m, session := setupGippityTest(t)
	m.respondToAllMessages = true
	m.classifyFunc = func(context.Context, *discordgo.MessageCreate) (bool, error) { return true, nil }
	m.generateAnswerFunc = func(_ *discordgo.MessageCreate, _ []string) (string, error) {
		return "", nil
	}
	describeCalled := false
	m.describeImagesFunc = func(urls []string) (string, error) {
		describeCalled = true
		if len(urls) != 1 || urls[0] != "https://cdn.example.com/photo.png" {
			t.Errorf("unexpected image URLs: %v", urls)
		}
		return "a cat on a mat", nil
	}

	msg := gippityTestMessageWithImage("check this out", "https://cdn.example.com/photo.png")
	if err := m.setUserConsent("user-1", true); err != nil {
		t.Fatalf("setUserConsent: %v", err)
	}

	m.onMessageCreate(session, msg)

	if !describeCalled {
		t.Error("describeImagesFunc should have been called for an answered image message")
	}

	var url, desc string
	err := queryRow(t, m.db, "SELECT attachment_url, image_description FROM gippity_chat_attachments WHERE message_id = ?", "img-msg-id").Scan(&url, &desc)
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
	m, _ := setupGippityTest(t)
	m.idToNameCache["user-1"] = "Alice"
	m.idToNameCache["channel-1"] = "general"
	m.idToNameCache["allowed-guild"] = "Test Guild"

	if _, err := execSQL(t, m.db, `INSERT INTO gippity_chat_history (user_id, channel_id, timestamp, message, message_id, guild_id, is_bot_mention) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"user-1", "channel-1", 1000, "hello from db", "db-msg-id", "allowed-guild", 0); err != nil {
		t.Fatalf("insert: %v", err)
	}

	msg, err := m.getMessageFromDatabase("db-msg-id")
	if err != nil {
		t.Fatalf("getMessageFromDatabase: %v", err)
	}
	if msg == nil {
		t.Fatal("expected non-nil message")
		return
	}
	if msg.Message != "hello from db" {
		t.Errorf("Message = %q, want %q", msg.Message, "hello from db")
	}
	if msg.UserID != "user-1" {
		t.Errorf("UserID = %q, want %q", msg.UserID, "user-1")
	}
}

func TestGetMessageFromDatabase_NotFound(t *testing.T) {
	m, _ := setupGippityTest(t)

	msg, err := m.getMessageFromDatabase("nonexistent-msg-id")
	if err != nil {
		t.Fatalf("getMessageFromDatabase returned unexpected error: %v", err)
	}
	if msg != nil {
		t.Errorf("expected nil message for nonexistent ID, got %+v", msg)
	}
}

func TestGenerateAnswer_NoReference_NoSystemNoteInjected(t *testing.T) {
	m, _ := setupGippityTest(t)

	fetchCalled := false
	m.fetchReferencedMessageFunc = func(_ *discordgo.Session, _ *discordgo.MessageReference) (*discordgo.Message, error) {
		fetchCalled = true
		return nil, nil
	}

	var capturedMessages []openai.ChatCompletionMessageParamUnion
	var capturedModel string
	m.chatCompletionFunc = func(_ context.Context, params openai.ChatCompletionNewParams) (*openai.ChatCompletion, error) {
		capturedMessages = params.Messages
		capturedModel = params.Model
		return &openai.ChatCompletion{
			Choices: []openai.ChatCompletionChoice{{Message: openai.ChatCompletionMessage{Content: "ok"}}},
		}, nil
	}

	msg := &discordgo.MessageCreate{
		Message: &discordgo.Message{
			ID:        "msg-no-ref",
			ChannelID: "channel-1",
			GuildID:   "allowed-guild",
			Content:   "hello <@bot-user>",
			Author:    &discordgo.User{ID: "user-1", Username: "Alice"},
			Mentions:  []*discordgo.User{{ID: "bot-user"}},
		},
	}

	if _, err := m.generateAnswer(msg, nil); err != nil {
		t.Fatalf("generateAnswer: %v", err)
	}

	if fetchCalled {
		t.Error("fetchReferencedMessageFunc must not be called when MessageReference is nil")
	}
	if capturedModel != m.llmClient.Model() {
		t.Errorf("model = %q, want configured model %q", capturedModel, m.llmClient.Model())
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
	m, _ := setupGippityTest(t)

	if !m.getUserPrivacy("opted-out-user") {
		t.Fatal("test precondition: opted-out-user should have privacy ON by default")
	}

	m.fetchReferencedMessageFunc = func(_ *discordgo.Session, _ *discordgo.MessageReference) (*discordgo.Message, error) {
		return &discordgo.Message{
			ID:      "ref-id",
			Content: "secret content",
			Author:  &discordgo.User{ID: "opted-out-user", Username: "Bob", Bot: false},
		}, nil
	}

	var capturedMessages []openai.ChatCompletionMessageParamUnion
	m.chatCompletionFunc = func(_ context.Context, params openai.ChatCompletionNewParams) (*openai.ChatCompletion, error) {
		capturedMessages = params.Messages
		return &openai.ChatCompletion{
			Choices: []openai.ChatCompletionChoice{{Message: openai.ChatCompletionMessage{Content: "ok"}}},
		}, nil
	}

	msg := &discordgo.MessageCreate{
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

	if _, err := m.generateAnswer(msg, nil); err != nil {
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
	m, _ := setupGippityTest(t)

	if err := m.setUserPrivacy("optin-user", false); err != nil {
		t.Fatalf("setUserPrivacy: %v", err)
	}

	m.fetchReferencedMessageFunc = func(_ *discordgo.Session, _ *discordgo.MessageReference) (*discordgo.Message, error) {
		return &discordgo.Message{
			ID:      "ref-id",
			Content: "visible content",
			Author:  &discordgo.User{ID: "optin-user", Username: "Carol", Bot: false},
		}, nil
	}

	var capturedMessages []openai.ChatCompletionMessageParamUnion
	m.chatCompletionFunc = func(_ context.Context, params openai.ChatCompletionNewParams) (*openai.ChatCompletion, error) {
		capturedMessages = params.Messages
		return &openai.ChatCompletion{
			Choices: []openai.ChatCompletionChoice{{Message: openai.ChatCompletionMessage{Content: "ok"}}},
		}, nil
	}

	msg := &discordgo.MessageCreate{
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

	if _, err := m.generateAnswer(msg, nil); err != nil {
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
	ts := time.Unix(1000, 0)
	return &discordgo.MessageUpdate{
		Message: &discordgo.Message{
			ID:              msgID,
			ChannelID:       "channel-1",
			GuildID:         guildID,
			Content:         content,
			EditedTimestamp: &ts,
			Author:          &discordgo.User{ID: authorID, Username: "Alice"},
		},
	}
}

func TestOnMessageUpdate_PrivacyEnabledUser_EditNotPersisted(t *testing.T) {
	m, session := setupGippityTest(t)

	if _, err := execSQL(t, m.db, `INSERT INTO gippity_chat_history (user_id, channel_id, timestamp, message, message_id, guild_id, is_bot_mention) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"user-1", "channel-1", 1000, "original", "edit-msg-id", "allowed-guild", 0); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// privacy on by default for user-1
	m.onMessageUpdate(session, gippityTestMessageUpdate("edit-msg-id", "edited content", "allowed-guild", "user-1"))

	var count int
	if err := queryRow(t, m.db, `SELECT COUNT(*) FROM gippity_chat_history_edits WHERE original_message_id = ?`, "edit-msg-id").Scan(&count); err != nil {
		t.Fatalf("query: %v", err)
	}
	if count != 0 {
		t.Errorf("edit should not be persisted for privacy-on user, got %d row(s)", count)
	}
}

func TestOnMessageUpdate_PrivacyDisabledUser_EditPersisted(t *testing.T) {
	m, session := setupGippityTest(t)

	if err := m.setUserPrivacy("user-1", false); err != nil {
		t.Fatalf("setUserPrivacy: %v", err)
	}
	if _, err := execSQL(t, m.db, `INSERT INTO gippity_chat_history (user_id, channel_id, timestamp, message, message_id, guild_id, is_bot_mention) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"user-1", "channel-1", 1000, "original", "edit-msg-id2", "allowed-guild", 0); err != nil {
		t.Fatalf("insert: %v", err)
	}

	m.onMessageUpdate(session, gippityTestMessageUpdate("edit-msg-id2", "first edit", "allowed-guild", "user-1"))
	m.onMessageUpdate(session, gippityTestMessageUpdate("edit-msg-id2", "second edit", "allowed-guild", "user-1"))

	var count int
	if err := queryRow(t, m.db, `SELECT COUNT(*) FROM gippity_chat_history_edits WHERE original_message_id = ?`, "edit-msg-id2").Scan(&count); err != nil {
		t.Fatalf("query: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 edit rows, got %d", count)
	}

	var version int
	var content string
	if err := queryRow(t, m.db, `SELECT version, edited_content FROM gippity_chat_history_edits WHERE original_message_id = ? ORDER BY version DESC LIMIT 1`, "edit-msg-id2").Scan(&version, &content); err != nil {
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
	m, session := setupGippityTest(t)

	msg := &discordgo.MessageUpdate{
		Message: &discordgo.Message{
			ID:        "nil-author-msg",
			ChannelID: "channel-1",
			GuildID:   "allowed-guild",
			Content:   "embed update",
			Author:    nil,
		},
	}
	m.onMessageUpdate(session, msg)

	var count int
	if err := queryRow(t, m.db, `SELECT COUNT(*) FROM gippity_chat_history_edits WHERE original_message_id = ?`, "nil-author-msg").Scan(&count); err != nil {
		t.Fatalf("query: %v", err)
	}
	if count != 0 {
		t.Errorf("nil-author update should not be persisted, got %d row(s)", count)
	}
}

func TestOnMessageUpdate_MessageNotInHistory_Skipped(t *testing.T) {
	m, session := setupGippityTest(t)

	if err := m.setUserPrivacy("user-1", false); err != nil {
		t.Fatalf("setUserPrivacy: %v", err)
	}

	m.onMessageUpdate(session, gippityTestMessageUpdate("unknown-msg-id", "edit of unknown", "allowed-guild", "user-1"))

	var count int
	if err := queryRow(t, m.db, `SELECT COUNT(*) FROM gippity_chat_history_edits WHERE original_message_id = ?`, "unknown-msg-id").Scan(&count); err != nil {
		t.Fatalf("query: %v", err)
	}
	if count != 0 {
		t.Errorf("edit of message not in gippity_chat_history should not be persisted, got %d row(s)", count)
	}
}

func TestGetLastNMessagesFromDatabase_ShowsLatestEdit(t *testing.T) {
	m, _ := setupGippityTest(t)
	m.idToNameCache["user-1"] = "Alice"
	m.idToNameCache["channel-1"] = "general"
	m.idToNameCache["allowed-guild"] = "Test Guild"

	if _, err := execSQL(t, m.db, `INSERT INTO gippity_chat_history (user_id, channel_id, timestamp, message, message_id, guild_id, is_bot_mention) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"user-1", "channel-1", 1000, "original message", "edited-hist-msg", "allowed-guild", 0); err != nil {
		t.Fatalf("insert gippity_chat_history: %v", err)
	}
	if _, err := execSQL(t, m.db, `INSERT INTO gippity_chat_history_edits (original_message_id, edited_content, version, edited_at) VALUES (?, ?, ?, ?)`,
		"edited-hist-msg", "v1 edit", 1, 1001); err != nil {
		t.Fatalf("insert edit v1: %v", err)
	}
	if _, err := execSQL(t, m.db, `INSERT INTO gippity_chat_history_edits (original_message_id, edited_content, version, edited_at) VALUES (?, ?, ?, ?)`,
		"edited-hist-msg", "v2 edit", 2, 1002); err != nil {
		t.Fatalf("insert edit v2: %v", err)
	}

	msgs, err := m.getLastNMessagesFromDatabase("channel-1", 10)
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

func TestGetMessageFromDatabase_ShowsLatestEdit(t *testing.T) {
	m, _ := setupGippityTest(t)

	if _, err := execSQL(t, m.db, `INSERT INTO gippity_chat_history (user_id, channel_id, timestamp, message, message_id, guild_id, is_bot_mention) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"user-1", "channel-1", 1000, "original message", "getmsg-edit-id", "allowed-guild", 0); err != nil {
		t.Fatalf("insert gippity_chat_history: %v", err)
	}
	if _, err := execSQL(t, m.db, `INSERT INTO gippity_chat_history_edits (original_message_id, edited_content, version, edited_at) VALUES (?, ?, ?, ?)`,
		"getmsg-edit-id", "v1 edit", 1, 1001); err != nil {
		t.Fatalf("insert edit v1: %v", err)
	}
	if _, err := execSQL(t, m.db, `INSERT INTO gippity_chat_history_edits (original_message_id, edited_content, version, edited_at) VALUES (?, ?, ?, ?)`,
		"getmsg-edit-id", "v2 edit", 2, 1002); err != nil {
		t.Fatalf("insert edit v2: %v", err)
	}

	msg, err := m.getMessageFromDatabase("getmsg-edit-id")
	if err != nil {
		t.Fatalf("getMessageFromDatabase: %v", err)
	}
	if msg.Message != "v2 edit" {
		t.Errorf("message content = %q, want %q", msg.Message, "v2 edit")
	}
}

func captureChatCompletion(m *Module, t *testing.T) *[]openai.ChatCompletionMessageParamUnion {
	t.Helper()
	captured := &[]openai.ChatCompletionMessageParamUnion{}
	m.chatCompletionFunc = func(_ context.Context, params openai.ChatCompletionNewParams) (*openai.ChatCompletion, error) {
		*captured = params.Messages
		return &openai.ChatCompletion{
			Choices: []openai.ChatCompletionChoice{{Message: openai.ChatCompletionMessage{Content: "ok"}}},
		}, nil
	}
	return captured
}

func reactionMessage(reactions ...*discordgo.MessageReactions) *discordgo.Message {
	return &discordgo.Message{Reactions: reactions}
}

func assertCapturedContains(t *testing.T, captured []openai.ChatCompletionMessageParamUnion, want string) {
	t.Helper()
	for _, msg := range captured {
		if msg.OfUser != nil {
			content := msg.OfUser.Content.OfString.Value
			if strings.Contains(content, want) {
				return
			}
		}
		if msg.OfSystem != nil {
			content := msg.OfSystem.Content.OfString.Value
			if strings.Contains(content, want) {
				return
			}
		}
		if msg.OfAssistant != nil {
			content := msg.OfAssistant.Content.OfString.Value
			if strings.Contains(content, want) {
				return
			}
		}
	}
	t.Errorf("captured messages do not contain %q", want)
}

func insertHistoryMessage(m *Module, t *testing.T, messageID, content string) {
	t.Helper()
	if _, err := execSQL(t, m.db,
		`INSERT INTO gippity_chat_history (user_id, channel_id, timestamp, message, message_id, guild_id, is_bot_mention) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"user-1", "channel-1", 1000, content, messageID, "allowed-guild", 0,
	); err != nil {
		t.Fatalf("insert gippity_chat_history: %v", err)
	}
}

func testGenerateMessage(messageID string) *discordgo.MessageCreate {
	return &discordgo.MessageCreate{
		Message: &discordgo.Message{
			ID:        messageID,
			ChannelID: "channel-1",
			GuildID:   "allowed-guild",
			Content:   "hello <@bot-user>",
			Author:    &discordgo.User{ID: "user-1", Username: "Alice"},
			Mentions:  []*discordgo.User{{ID: "bot-user"}},
		},
	}
}

func TestGenerateAnswer_HistoryIncludesReactionSummary(t *testing.T) {
	m, _ := setupGippityTest(t)
	m.idToNameCache["user-1"] = "Alice"
	m.idToNameCache["channel-1"] = "general"
	m.idToNameCache["allowed-guild"] = "Test Guild"
	if err := m.setUserPrivacy("user-1", false); err != nil {
		t.Fatalf("setUserPrivacy: %v", err)
	}

	insertHistoryMessage(m, t, "hist-1", "hello there")
	m.fetchMessageReactionsFunc = func(_ *discordgo.Session, _, messageID string) (*discordgo.Message, error) {
		if messageID != "hist-1" {
			return &discordgo.Message{}, nil
		}
		return reactionMessage(
			&discordgo.MessageReactions{Count: 1, Emoji: &discordgo.Emoji{Name: "😂"}},
			&discordgo.MessageReactions{Count: 3, Emoji: &discordgo.Emoji{Name: "👍"}},
		), nil
	}
	captured := captureChatCompletion(m, t)

	if _, err := m.generateAnswer(testGenerateMessage("current-msg"), nil); err != nil {
		t.Fatalf("generateAnswer: %v", err)
	}
	assertCapturedContains(t, *captured, "hello there [reactions: 👍×3 😂×1]")
}

func TestGenerateAnswer_ReferencedMessageIncludesReactionSummary(t *testing.T) {
	m, _ := setupGippityTest(t)
	m.idToNameCache["channel-1"] = "general"
	m.idToNameCache["allowed-guild"] = "Test Guild"
	if err := m.setUserPrivacy("other-user", false); err != nil {
		t.Fatalf("setUserPrivacy: %v", err)
	}

	m.fetchReferencedMessageFunc = func(_ *discordgo.Session, _ *discordgo.MessageReference) (*discordgo.Message, error) {
		return &discordgo.Message{
			ID:        "ref-id",
			ChannelID: "channel-1",
			Content:   "referenced text",
			Author:    &discordgo.User{ID: "other-user", Username: "Bob"},
		}, nil
	}
	m.fetchMessageReactionsFunc = func(_ *discordgo.Session, _, messageID string) (*discordgo.Message, error) {
		if messageID != "ref-id" {
			return &discordgo.Message{}, nil
		}
		return reactionMessage(&discordgo.MessageReactions{Count: 2, Emoji: &discordgo.Emoji{Name: "✅"}}), nil
	}
	captured := captureChatCompletion(m, t)

	msg := testGenerateMessage("current-ref-msg")
	msg.MessageReference = &discordgo.MessageReference{MessageID: "ref-id", ChannelID: "channel-1", GuildID: "allowed-guild"}

	if _, err := m.generateAnswer(msg, nil); err != nil {
		t.Fatalf("generateAnswer: %v", err)
	}
	assertCapturedContains(t, *captured, "[System note: User is replying to a message from Bob: referenced text] [reactions: ✅×2]")
}

func TestGenerateAnswer_ReactionFetchError_OmitsReactionContext(t *testing.T) {
	m, _ := setupGippityTest(t)
	m.idToNameCache["user-1"] = "Alice"
	m.idToNameCache["channel-1"] = "general"
	m.idToNameCache["allowed-guild"] = "Test Guild"
	if err := m.setUserPrivacy("user-1", false); err != nil {
		t.Fatalf("setUserPrivacy: %v", err)
	}

	insertHistoryMessage(m, t, "err-msg", "some content")
	m.fetchMessageReactionsFunc = func(_ *discordgo.Session, _, _ string) (*discordgo.Message, error) {
		return nil, sql.ErrConnDone
	}
	captured := captureChatCompletion(m, t)

	if _, err := m.generateAnswer(testGenerateMessage("current-err"), nil); err != nil {
		t.Fatalf("generateAnswer must not fail on reaction fetch error: %v", err)
	}
	for _, msg := range *captured {
		if msg.OfUser != nil && strings.Contains(msg.OfUser.Content.OfString.Value, "[reactions:") {
			t.Error("reaction context should be omitted when fetch fails")
		}
	}
}

func TestGenerateAnswer_NoReactions_NoSummary(t *testing.T) {
	m, _ := setupGippityTest(t)
	m.idToNameCache["user-1"] = "Alice"
	m.idToNameCache["channel-1"] = "general"
	m.idToNameCache["allowed-guild"] = "Test Guild"
	if err := m.setUserPrivacy("user-1", false); err != nil {
		t.Fatalf("setUserPrivacy: %v", err)
	}

	insertHistoryMessage(m, t, "no-react-msg", "plain content")
	captured := captureChatCompletion(m, t)

	if _, err := m.generateAnswer(testGenerateMessage("current-no-react"), nil); err != nil {
		t.Fatalf("generateAnswer: %v", err)
	}
	for _, msg := range *captured {
		if msg.OfUser != nil && strings.Contains(msg.OfUser.Content.OfString.Value, "[reactions:") {
			t.Error("reaction summary should be absent when message has no reactions")
		}
	}
}

func TestGenerateAnswer_PrivacyUser_ReactionCountWithoutAttribution(t *testing.T) {
	m, _ := setupGippityTest(t)
	m.idToNameCache["user-1"] = "Alice"
	m.idToNameCache["channel-1"] = "general"
	m.idToNameCache["allowed-guild"] = "Test Guild"

	insertHistoryMessage(m, t, "private-msg", "secret content")
	m.fetchMessageReactionsFunc = func(_ *discordgo.Session, _, messageID string) (*discordgo.Message, error) {
		if messageID != "private-msg" {
			return &discordgo.Message{}, nil
		}
		return reactionMessage(&discordgo.MessageReactions{Count: 5, Emoji: &discordgo.Emoji{Name: "👍"}}), nil
	}
	captured := captureChatCompletion(m, t)

	if _, err := m.generateAnswer(testGenerateMessage("current-private"), nil); err != nil {
		t.Fatalf("generateAnswer: %v", err)
	}
	assertCapturedContains(t, *captured, "[Anonymisierte Nachricht] [reactions: 👍×5]")
	for _, msg := range *captured {
		if msg.OfUser != nil && strings.Contains(msg.OfUser.Content.OfString.Value, "secret content") {
			t.Error("privacy-opted-in message content must stay hidden")
		}
	}
}

func TestGenerateAnswer_DeduplicatesReactionFetchForReferencedMessage(t *testing.T) {
	m, _ := setupGippityTest(t)
	m.idToNameCache["user-1"] = "Alice"
	m.idToNameCache["channel-1"] = "general"
	m.idToNameCache["allowed-guild"] = "Test Guild"
	if err := m.setUserPrivacy("user-1", false); err != nil {
		t.Fatalf("setUserPrivacy: %v", err)
	}

	insertHistoryMessage(m, t, "shared-msg", "shared content")
	m.fetchReferencedMessageFunc = func(_ *discordgo.Session, _ *discordgo.MessageReference) (*discordgo.Message, error) {
		return &discordgo.Message{
			ID:        "shared-msg",
			ChannelID: "channel-1",
			Content:   "shared content",
			Author:    &discordgo.User{ID: "other-user", Username: "Bob"},
		}, nil
	}
	fetches := map[string]int{}
	m.fetchMessageReactionsFunc = func(_ *discordgo.Session, _, messageID string) (*discordgo.Message, error) {
		fetches[messageID]++
		return reactionMessage(&discordgo.MessageReactions{Count: 1, Emoji: &discordgo.Emoji{Name: "👍"}}), nil
	}
	captureChatCompletion(m, t)

	msg := testGenerateMessage("current-dedup")
	msg.MessageReference = &discordgo.MessageReference{MessageID: "shared-msg", ChannelID: "channel-1", GuildID: "allowed-guild"}

	if _, err := m.generateAnswer(msg, nil); err != nil {
		t.Fatalf("generateAnswer: %v", err)
	}
	if fetches["shared-msg"] != 1 {
		t.Errorf("reaction fetch for shared message = %d, want 1", fetches["shared-msg"])
	}
}

func TestGetLastNMessagesFromDatabase_IncludesImageDescriptions(t *testing.T) {
	m, _ := setupGippityTest(t)
	m.idToNameCache["user-1"] = "Alice"
	m.idToNameCache["channel-1"] = "general"
	m.idToNameCache["allowed-guild"] = "Test Guild"

	if _, err := execSQL(t, m.db, `INSERT INTO gippity_chat_history (user_id, channel_id, timestamp, message, message_id, guild_id, is_bot_mention) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"user-1", "channel-1", 1000, "look at this", "msg-with-img", "allowed-guild", 0); err != nil {
		t.Fatalf("insert gippity_chat_history: %v", err)
	}
	if _, err := execSQL(t, m.db, `INSERT INTO gippity_chat_attachments (message_id, attachment_url, image_description) VALUES (?, ?, ?)`,
		"msg-with-img", "https://cdn.example.com/photo.png", "a fluffy dog"); err != nil {
		t.Fatalf("insert gippity_chat_attachments: %v", err)
	}

	msgs, err := m.getLastNMessagesFromDatabase("channel-1", 10)
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

func TestSendReply_PostsInChannelWhenThreadRepliesDisabled(t *testing.T) {
	m, session := setupGippityTest(t)
	m.replyInThread = false

	var threadStarts int
	var sentChannel string
	m.messageThreadStartFunc = func(_ *discordgo.Session, _, _ string, _ *discordgo.ThreadStart) (*discordgo.Channel, error) {
		threadStarts++
		return &discordgo.Channel{ID: "thread-1"}, nil
	}
	m.messageSendFunc = func(_ *discordgo.Session, channelID, _ string) (*discordgo.Message, error) {
		sentChannel = channelID
		return &discordgo.Message{}, nil
	}

	if err := m.sendReply(gippityTestMessage("hey <@bot-user>"), "answer"); err != nil {
		t.Fatalf("sendReply: %v", err)
	}
	if threadStarts != 0 {
		t.Errorf("thread starts = %d, want 0", threadStarts)
	}
	if sentChannel != "channel-1" {
		t.Errorf("reply channel = %q, want %q", sentChannel, "channel-1")
	}
	_ = session
}

func TestSendReply_StartsThreadWhenEnabled(t *testing.T) {
	m, _ := setupGippityTest(t)
	m.replyInThread = true

	var startChannel, startMessage, name string
	var sentChannel string
	m.messageThreadStartFunc = func(_ *discordgo.Session, channelID, messageID string, data *discordgo.ThreadStart) (*discordgo.Channel, error) {
		startChannel, startMessage, name = channelID, messageID, data.Name
		return &discordgo.Channel{ID: "thread-42"}, nil
	}
	m.messageSendFunc = func(_ *discordgo.Session, channelID, _ string) (*discordgo.Message, error) {
		sentChannel = channelID
		return &discordgo.Message{}, nil
	}

	msg := gippityTestMessage("hey <@123456789> wie geht das")
	if err := m.sendReply(msg, "answer"); err != nil {
		t.Fatalf("sendReply: %v", err)
	}
	if startChannel != "channel-1" || startMessage != msg.ID {
		t.Errorf("thread started on %q/%q, want %q/%q", startChannel, startMessage, "channel-1", msg.ID)
	}
	if name != "hey wie geht das" {
		t.Errorf("thread name = %q, want %q", name, "hey wie geht das")
	}
	if sentChannel != "thread-42" {
		t.Errorf("reply channel = %q, want %q", sentChannel, "thread-42")
	}
}

func TestSendReply_AnswersInPlaceInsideExistingThread(t *testing.T) {
	m, _ := setupGippityTest(t)
	m.replyInThread = true
	m.channelIsThreadFunc = func(_ *discordgo.Session, _ string) bool { return true }

	var threadStarts int
	var sentChannel string
	m.messageThreadStartFunc = func(_ *discordgo.Session, _, _ string, _ *discordgo.ThreadStart) (*discordgo.Channel, error) {
		threadStarts++
		return &discordgo.Channel{ID: "thread-42"}, nil
	}
	m.messageSendFunc = func(_ *discordgo.Session, channelID, _ string) (*discordgo.Message, error) {
		sentChannel = channelID
		return &discordgo.Message{}, nil
	}

	msg := gippityTestMessage("hey <@bot-user>")
	msg.ChannelID = "existing-thread"

	if err := m.sendReply(msg, "answer"); err != nil {
		t.Fatalf("sendReply: %v", err)
	}
	if threadStarts != 0 {
		t.Errorf("thread starts = %d, want 0 for a message already in a thread", threadStarts)
	}
	if sentChannel != "existing-thread" {
		t.Errorf("reply channel = %q, want %q", sentChannel, "existing-thread")
	}
}

func TestSendReply_FallsBackToChannelWhenThreadStartFails(t *testing.T) {
	tests := []struct {
		name   string
		thread func(*discordgo.Session, string, string, *discordgo.ThreadStart) (*discordgo.Channel, error)
	}{
		{
			name: "start returns error",
			thread: func(_ *discordgo.Session, _, _ string, _ *discordgo.ThreadStart) (*discordgo.Channel, error) {
				return nil, errors.New("missing create public threads permission")
			},
		},
		{
			name: "start returns no channel",
			thread: func(_ *discordgo.Session, _, _ string, _ *discordgo.ThreadStart) (*discordgo.Channel, error) {
				return nil, nil
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := setupGippityTest(t)
			m.replyInThread = true
			m.messageThreadStartFunc = tc.thread

			var sentChannel string
			m.messageSendFunc = func(_ *discordgo.Session, channelID, _ string) (*discordgo.Message, error) {
				sentChannel = channelID
				return &discordgo.Message{}, nil
			}

			if err := m.sendReply(gippityTestMessage("hey <@bot-user>"), "answer"); err != nil {
				t.Fatalf("sendReply: %v", err)
			}
			if sentChannel != "channel-1" {
				t.Errorf("reply channel = %q, want channel fallback %q", sentChannel, "channel-1")
			}
		})
	}
}

func TestOnMessageCreate_RepliesInThreadWhenConfigured(t *testing.T) {
	m, session := setupGippityTest(t)
	m.replyInThread = true
	m.generateAnswerFunc = func(_ *discordgo.MessageCreate, _ []string) (string, error) {
		return "answer", nil
	}

	var sentChannel string
	m.messageSendFunc = func(_ *discordgo.Session, channelID, _ string) (*discordgo.Message, error) {
		sentChannel = channelID
		return &discordgo.Message{}, nil
	}
	m.messageThreadStartFunc = func(_ *discordgo.Session, _, _ string, _ *discordgo.ThreadStart) (*discordgo.Channel, error) {
		return &discordgo.Channel{ID: "thread-9"}, nil
	}

	m.onMessageCreate(session, gippityTestMessage("hey <@bot-user>", &discordgo.User{ID: "bot-user"}))

	if sentChannel != "thread-9" {
		t.Errorf("reply channel = %q, want thread %q", sentChannel, "thread-9")
	}
}

func TestOnMessageCreate_RateLimitNoticeFollowsThreadReplySetting(t *testing.T) {
	m, session := setupGippityTest(t)
	m.replyInThread = true
	m.userMessageLimit = 1
	m.generateAnswerFunc = func(_ *discordgo.MessageCreate, _ []string) (string, error) {
		return "", nil
	}

	var sentChannel string
	m.messageSendFunc = func(_ *discordgo.Session, channelID, _ string) (*discordgo.Message, error) {
		sentChannel = channelID
		return &discordgo.Message{}, nil
	}
	m.messageThreadStartFunc = func(_ *discordgo.Session, _, _ string, _ *discordgo.ThreadStart) (*discordgo.Channel, error) {
		return &discordgo.Channel{ID: "thread-9"}, nil
	}

	msg := gippityTestMessage("hey <@bot-user>", &discordgo.User{ID: "bot-user"})
	m.onMessageCreate(session, msg) // first mention: allowed
	sentChannel = ""
	m.onMessageCreate(session, msg) // second mention: hits the limit of 1

	if sentChannel != "thread-9" {
		t.Errorf("rate limit notice sent to %q, want thread %q", sentChannel, "thread-9")
	}
}

func TestThreadName(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{"strips mentions", "hey <@123> <@!456> <#789> <@&42> frage", "hey frage"},
		{"strips backticks and newlines", "```go\nfunc main() {}\n```", "go func main() {}"},
		{"collapses whitespace", "  hallo \t welt  ", "hallo welt"},
		{"blank falls back", "   ", defaultThreadName},
		{"mention only falls back", "<@123>", defaultThreadName},
		{"keeps plain text", "wie baue ich das", "wie baue ich das"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := threadName(tc.content); got != tc.want {
				t.Errorf("threadName(%q) = %q, want %q", tc.content, got, tc.want)
			}
		})
	}
}

func TestThreadName_TruncatesToDiscordLimit(t *testing.T) {
	got := threadName(strings.Repeat("ä", threadNameMaxLength+50))
	if runes := []rune(got); len(runes) != threadNameMaxLength {
		t.Errorf("thread name length = %d runes, want %d", len(runes), threadNameMaxLength)
	}
}

func TestThreadName_DropsZeroWidthCharacters(t *testing.T) {
	got := threadName("hal\u200blo\u200dwelt")
	if got != "hallowelt" {
		t.Errorf("threadName = %q, want %q", got, "hallowelt")
	}
}

func TestParseClassifierAnswer(t *testing.T) {
	tests := []struct {
		in      string
		want    bool
		wantErr bool
	}{
		{"YES", true, false},
		{"yes", true, false},
		{" YES \n", true, false},
		{"NO", false, false},
		{"no", false, false},
		{"maybe", false, true},
		{"", false, true},
		{"YES NO", false, true},
	}
	for _, tc := range tests {
		got, err := parseClassifierAnswer(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseClassifierAnswer(%q) expected error", tc.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseClassifierAnswer(%q) unexpected error: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("parseClassifierAnswer(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestClassifyShouldRespond_UsesDeterministicClassifierParams(t *testing.T) {
	m, _ := setupGippityTest(t)
	m.classifierModel = "classifier-model-x"

	var gotParams openai.ChatCompletionNewParams
	m.chatCompletionFunc = func(_ context.Context, params openai.ChatCompletionNewParams) (*openai.ChatCompletion, error) {
		gotParams = params
		return &openai.ChatCompletion{
			Choices: []openai.ChatCompletionChoice{{Message: openai.ChatCompletionMessage{Content: "YES"}}},
		}, nil
	}

	got, err := m.classifyShouldRespond(context.Background(), gippityTestMessage("should the bot answer this?"))
	if err != nil {
		t.Fatalf("classifyShouldRespond: %v", err)
	}
	if !got {
		t.Error("expected YES to map to a positive decision")
	}
	if gotParams.Model != "classifier-model-x" {
		t.Errorf("model = %q, want classifier-model-x", gotParams.Model)
	}
	if !gotParams.Temperature.Valid() || gotParams.Temperature.Value != 0 {
		t.Errorf("temperature = %+v, want 0", gotParams.Temperature)
	}
	if gotParams.MaxCompletionTokens.Value != classifierMaxTokens {
		t.Errorf("max_completion_tokens = %d, want %d", gotParams.MaxCompletionTokens.Value, classifierMaxTokens)
	}
}

func TestClassifyShouldRespond_FallsBackToLLMModel(t *testing.T) {
	m, _ := setupGippityTest(t)
	m.classifierModel = ""

	var gotModel string
	m.chatCompletionFunc = func(_ context.Context, params openai.ChatCompletionNewParams) (*openai.ChatCompletion, error) {
		gotModel = params.Model
		return &openai.ChatCompletion{
			Choices: []openai.ChatCompletionChoice{{Message: openai.ChatCompletionMessage{Content: "NO"}}},
		}, nil
	}

	if _, err := m.classifyShouldRespond(context.Background(), gippityTestMessage("hello")); err != nil {
		t.Fatalf("classifyShouldRespond: %v", err)
	}
	if gotModel != m.llmClient.Model() {
		t.Errorf("model = %q, want configured model %q", gotModel, m.llmClient.Model())
	}
}

func TestClassifierBotNames(t *testing.T) {
	m, session := setupGippityTest(t)
	session.State.User = &discordgo.User{ID: "bot-user", Username: "gustaff", GlobalName: "Gustaff"}
	m.classifierNames = []string{" gustaff ", "", "Gippity"}

	if got, want := strings.Join(m.classifierBotNames(), ","), "gustaff,Gippity"; got != want {
		t.Errorf("classifierBotNames() = %q, want %q", got, want)
	}

	m.classifierNames = nil
	session.State.User = nil
	if got := m.classifierBotNames(); len(got) != 0 {
		t.Errorf("classifierBotNames() = %v, want empty", got)
	}
}

func TestClassifierSystemPromptNamesBot(t *testing.T) {
	if got := classifierSystemPrompt(nil); got != classifierSystemPromptBase {
		t.Errorf("classifierSystemPrompt(nil) = %q, want the base prompt", got)
	}
	if got := classifierSystemPrompt([]string{"gustaff"}); !strings.Contains(got, "gustaff") {
		t.Errorf("classifierSystemPrompt = %q, want it to name the bot", got)
	}
}

func TestClassifyShouldRespond_PromptNamesBot(t *testing.T) {
	m, session := setupGippityTest(t)
	session.State.User = &discordgo.User{ID: "bot-user", Username: "gustaff"}
	m.classifierNames = []string{"Gippity"}

	var system string
	m.chatCompletionFunc = func(_ context.Context, params openai.ChatCompletionNewParams) (*openai.ChatCompletion, error) {
		for _, msg := range params.Messages {
			if msg.OfSystem != nil {
				system = msg.OfSystem.Content.OfString.Value
			}
		}
		return &openai.ChatCompletion{
			Choices: []openai.ChatCompletionChoice{{Message: openai.ChatCompletionMessage{Content: "YES"}}},
		}, nil
	}

	if _, err := m.classifyShouldRespond(context.Background(), gippityTestMessage("gustaff, was ist das?")); err != nil {
		t.Fatalf("classifyShouldRespond: %v", err)
	}
	for _, want := range []string{"gustaff", "Gippity", "by name"} {
		if !strings.Contains(system, want) {
			t.Errorf("classifier prompt %q lacks %q", system, want)
		}
	}
}

func TestOnMessageCreate_NonMentionDisabledNeverClassifies(t *testing.T) {
	m, session := setupGippityTest(t)
	m.respondToAllMessages = false
	m.classifyFunc = func(context.Context, *discordgo.MessageCreate) (bool, error) {
		t.Fatal("classifier must not run when respond_to_all_messages is off")
		return false, nil
	}
	m.generateAnswerFunc = func(_ *discordgo.MessageCreate, _ []string) (string, error) {
		t.Fatal("generateAnswerFunc must not run for a disabled non-mention message")
		return "", nil
	}

	m.onMessageCreate(session, gippityTestMessage("a question without a mention"))
}

func TestOnMessageCreate_NonMentionWithoutConsentNeverClassifies(t *testing.T) {
	m, session := setupGippityTest(t)
	m.respondToAllMessages = true
	m.classifyFunc = func(context.Context, *discordgo.MessageCreate) (bool, error) {
		t.Fatal("classifier must not run without the user's consent")
		return false, nil
	}

	m.onMessageCreate(session, gippityTestMessage("a question without a mention"))
}

func TestOnMessageCreate_NonMentionPositiveDecisionAnswers(t *testing.T) {
	m, session := setupGippityTest(t)
	m.respondToAllMessages = true
	m.classifyFunc = func(context.Context, *discordgo.MessageCreate) (bool, error) { return true, nil }
	m.generateAnswerFunc = func(_ *discordgo.MessageCreate, _ []string) (string, error) {
		return "the answer", nil
	}
	var sent string
	m.messageSendFunc = func(_ *discordgo.Session, _ string, content string) (*discordgo.Message, error) {
		sent = content
		return &discordgo.Message{}, nil
	}
	if err := m.setUserConsent("user-1", true); err != nil {
		t.Fatalf("setUserConsent: %v", err)
	}

	m.onMessageCreate(session, gippityTestMessage("what is 2+2?"))

	if sent != "the answer" {
		t.Errorf("sent = %q, want %q", sent, "the answer")
	}
}

func TestOnMessageCreate_NonMentionNegativeDecisionStaysSilent(t *testing.T) {
	m, session := setupGippityTest(t)
	m.respondToAllMessages = true
	m.classifyFunc = func(context.Context, *discordgo.MessageCreate) (bool, error) { return false, nil }
	m.generateAnswerFunc = func(_ *discordgo.MessageCreate, _ []string) (string, error) {
		t.Fatal("generateAnswerFunc must not run for a negative decision")
		return "", nil
	}
	m.messageSendFunc = func(_ *discordgo.Session, _ string, _ string) (*discordgo.Message, error) {
		t.Fatal("no reply should be sent for a negative decision")
		return &discordgo.Message{}, nil
	}
	if err := m.setUserConsent("user-1", true); err != nil {
		t.Fatalf("setUserConsent: %v", err)
	}

	m.onMessageCreate(session, gippityTestMessage("just chatting"))
}

func TestOnMessageCreate_NonMentionClassifierErrorFailsClosed(t *testing.T) {
	m, session := setupGippityTest(t)
	m.respondToAllMessages = true
	m.classifyFunc = func(context.Context, *discordgo.MessageCreate) (bool, error) {
		return false, errors.New("provider unavailable")
	}
	m.generateAnswerFunc = func(_ *discordgo.MessageCreate, _ []string) (string, error) {
		t.Fatal("generateAnswerFunc must not run when the classifier fails")
		return "", nil
	}
	if err := m.setUserConsent("user-1", true); err != nil {
		t.Fatalf("setUserConsent: %v", err)
	}

	m.onMessageCreate(session, gippityTestMessage("does the bot answer?"))
}

func TestOnMessageCreate_MentionBypassesClassifierAndConsent(t *testing.T) {
	m, session := setupGippityTest(t)
	m.respondToAllMessages = false
	m.classifyFunc = func(context.Context, *discordgo.MessageCreate) (bool, error) {
		t.Fatal("a direct mention must skip the classifier")
		return false, nil
	}
	m.generateAnswerFunc = func(_ *discordgo.MessageCreate, _ []string) (string, error) {
		return "mentioned answer", nil
	}
	var sent string
	m.messageSendFunc = func(_ *discordgo.Session, _ string, content string) (*discordgo.Message, error) {
		sent = content
		return &discordgo.Message{}, nil
	}

	m.onMessageCreate(session, gippityTestMessage("hey <@bot-user>", &discordgo.User{ID: "bot-user"}))

	if sent != "mentioned answer" {
		t.Errorf("sent = %q, want %q", sent, "mentioned answer")
	}
}

// debugLoggingForTest enables debug-level logging for one test and restores the
// previous default on cleanup.
func debugLoggingForTest(t *testing.T) {
	t.Helper()
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })
}

func TestOnMessageCreate_DebugChannelRestrictsToConfiguredChannel(t *testing.T) {
	debugLoggingForTest(t)
	m, session := setupGippityTest(t)
	m.debugChannel = "dev-channel"

	called := 0
	m.generateAnswerFunc = func(_ *discordgo.MessageCreate, _ []string) (string, error) {
		called++
		return "", nil
	}

	other := gippityTestMessage("hey <@bot-user>", &discordgo.User{ID: "bot-user"})
	other.ChannelID = "other-channel"
	m.onMessageCreate(session, other)
	if called != 0 {
		t.Fatalf("answered in a non-debug channel: %d", called)
	}

	dev := gippityTestMessage("hey <@bot-user>", &discordgo.User{ID: "bot-user"})
	dev.ChannelID = "dev-channel"
	m.onMessageCreate(session, dev)
	if called != 1 {
		t.Fatalf("did not answer in the configured debug channel: %d", called)
	}
}

func TestOnMessageCreate_EmptyDebugChannelDoesNotRestrict(t *testing.T) {
	debugLoggingForTest(t)
	m, session := setupGippityTest(t)
	m.debugChannel = ""

	called := 0
	m.generateAnswerFunc = func(_ *discordgo.MessageCreate, _ []string) (string, error) {
		called++
		return "", nil
	}

	m.onMessageCreate(session, gippityTestMessage("hey <@bot-user>", &discordgo.User{ID: "bot-user"}))
	if called != 1 {
		t.Fatalf("empty debug_channel should not restrict: %d", called)
	}
}

func TestOnMessageCreate_NonMentionRespectsClassifierBudget(t *testing.T) {
	m, session := setupGippityTest(t)
	m.respondToAllMessages = true
	m.classifierRateLimit = 1
	calls := 0
	m.classifyFunc = func(context.Context, *discordgo.MessageCreate) (bool, error) {
		calls++
		return false, nil
	}
	if err := m.setUserConsent("user-1", true); err != nil {
		t.Fatalf("setUserConsent: %v", err)
	}

	m.onMessageCreate(session, gippityTestMessage("first non-mention"))
	m.onMessageCreate(session, gippityTestMessage("second non-mention"))

	if calls != 1 {
		t.Errorf("classifier calls = %d, want 1 (budget of 1 per hour)", calls)
	}
}

func TestOnMessageCreate_NonMentionReplyBudgetSkipsSilently(t *testing.T) {
	m, session := setupGippityTest(t)
	m.respondToAllMessages = true
	m.userMessageLimit = 1
	m.classifyFunc = func(context.Context, *discordgo.MessageCreate) (bool, error) { return true, nil }
	m.generateAnswerFunc = func(_ *discordgo.MessageCreate, _ []string) (string, error) {
		return "answer", nil
	}
	sends := 0
	m.messageSendFunc = func(_ *discordgo.Session, _ string, _ string) (*discordgo.Message, error) {
		sends++
		return &discordgo.Message{}, nil
	}
	if err := m.setUserConsent("user-1", true); err != nil {
		t.Fatalf("setUserConsent: %v", err)
	}

	m.onMessageCreate(session, gippityTestMessage("first"))
	m.onMessageCreate(session, gippityTestMessage("second"))

	if sends != 1 {
		t.Errorf("sends = %d, want 1 (second hits the reply budget and skips silently)", sends)
	}
}

func TestOnMessageCreate_UnsupportedTypeNeverClassifies(t *testing.T) {
	m, session := setupGippityTest(t)
	m.respondToAllMessages = true
	m.classifyFunc = func(context.Context, *discordgo.MessageCreate) (bool, error) {
		t.Fatal("system messages must not reach the classifier")
		return false, nil
	}
	if err := m.setUserConsent("user-1", true); err != nil {
		t.Fatalf("setUserConsent: %v", err)
	}

	msg := gippityTestMessage("joined")
	msg.Type = discordgo.MessageTypeGuildMemberJoin
	m.onMessageCreate(session, msg)
}

func TestOnMessageCreate_BotAuthorNeverClassifies(t *testing.T) {
	m, session := setupGippityTest(t)
	m.respondToAllMessages = true
	m.classifyFunc = func(context.Context, *discordgo.MessageCreate) (bool, error) {
		t.Fatal("bot messages must not reach the classifier")
		return false, nil
	}

	msg := gippityTestMessage("beep")
	msg.Author.Bot = true
	m.onMessageCreate(session, msg)
}

func TestSetAndGetUserConsent(t *testing.T) {
	m, _ := setupGippityTest(t)

	if m.getUserConsent("user-a") {
		t.Error("getUserConsent for unknown user should default to false")
	}
	if err := m.setUserConsent("user-a", true); err != nil {
		t.Fatalf("setUserConsent: %v", err)
	}
	if !m.getUserConsent("user-a") {
		t.Error("getUserConsent should return true after opt-in")
	}
	if err := m.setUserConsent("user-a", false); err != nil {
		t.Fatalf("setUserConsent: %v", err)
	}
	if m.getUserConsent("user-a") {
		t.Error("getUserConsent should return false after opt-out")
	}
}

func TestCommands_IncludeNonmentionSubcommand(t *testing.T) {
	m := New()
	var names []string
	for _, cmd := range m.Commands() {
		if cmd.Name != "gippity" {
			continue
		}
		for _, opt := range cmd.Options {
			names = append(names, opt.Name)
		}
	}
	for _, want := range []string{"privacy", "nonmention"} {
		found := false
		for _, name := range names {
			if name == want {
				found = true
			}
		}
		if !found {
			t.Errorf("gippity command missing subcommand %q; have %v", want, names)
		}
	}
}

func TestDebugLogs_OmitMessageContent(t *testing.T) {
	previous := slog.Default()
	var logs strings.Builder
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	m, session := setupGippityTest(t)
	m.generateAnswerFunc = func(_ *discordgo.MessageCreate, _ []string) (string, error) {
		return "secret-answer-text", nil
	}
	m.messageSendFunc = func(_ *discordgo.Session, _, _ string) (*discordgo.Message, error) {
		return &discordgo.Message{}, nil
	}
	m.onMessageCreate(session, gippityTestMessage("secret-user-text <@bot-user>", &discordgo.User{ID: "bot-user"}))

	insertHistoryMessage(m, t, "hist-secret", "secret-history-text")
	captureChatCompletion(m, t)
	if _, err := m.generateAnswer(testGenerateMessage("current-secret"), nil); err != nil {
		t.Fatalf("generateAnswer: %v", err)
	}

	out := logs.String()
	if !strings.Contains(out, "Message received") || !strings.Contains(out, "Generated answer") || !strings.Contains(out, "prompt built") {
		t.Fatalf("expected debug logs were not emitted: %s", out)
	}
	for _, secret := range []string{"secret-user-text", "secret-answer-text", "secret-history-text", "hello <@bot-user>"} {
		if strings.Contains(out, secret) {
			t.Errorf("debug log leaks %q: %s", secret, out)
		}
	}
}

func TestOnMessageCreate_StripsOuterBracketsFromAnswer(t *testing.T) {
	m, session := setupGippityTest(t)
	m.generateAnswerFunc = func(_ *discordgo.MessageCreate, _ []string) (string, error) {
		return "[answer]", nil
	}

	var sent string
	m.messageSendFunc = func(_ *discordgo.Session, _, content string) (*discordgo.Message, error) {
		sent = content
		return &discordgo.Message{}, nil
	}

	m.onMessageCreate(session, gippityTestMessage("hey <@bot-user>", &discordgo.User{ID: "bot-user"}))

	if sent != "answer" {
		t.Errorf("sent = %q, want %q", sent, "answer")
	}
}
