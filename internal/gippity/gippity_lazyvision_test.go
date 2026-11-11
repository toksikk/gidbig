package gippity

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	openai "github.com/openai/openai-go/v3"
)

// lazyTestImagePost stores an unmentioned image post through onMessageCreate.
func lazyTestImagePost(t *testing.T, m *Module, session *discordgo.Session, id, userID, url string) {
	t.Helper()
	msg := gippityTestMessageWithImage("look", url)
	msg.ID = id
	msg.Author = &discordgo.User{ID: userID, Username: userID}
	m.onMessageCreate(session, msg)
}

func lazyTestQuestion(id string, ref string) *discordgo.MessageCreate {
	msg := &discordgo.MessageCreate{Message: &discordgo.Message{
		ID:        id,
		ChannelID: "channel-1",
		GuildID:   "allowed-guild",
		Content:   "what is on the picture? <@bot-user>",
		Author:    &discordgo.User{ID: "asker", Username: "Asker"},
		Mentions:  []*discordgo.User{{ID: "bot-user"}},
	}}
	if ref != "" {
		msg.MessageReference = &discordgo.MessageReference{MessageID: ref, ChannelID: "channel-1", GuildID: "allowed-guild"}
	}
	return msg
}

func lazyTestCapturePrompt(m *Module) *string {
	prompt := new(string)
	m.chatCompletionFunc = func(_ context.Context, params openai.ChatCompletionNewParams) (*openai.ChatCompletion, error) {
		var sb strings.Builder
		for _, msg := range params.Messages {
			switch {
			case msg.OfSystem != nil:
				sb.WriteString(msg.OfSystem.Content.OfString.Value + "\n")
			case msg.OfUser != nil:
				sb.WriteString(msg.OfUser.Content.OfString.Value + "\n")
			}
		}
		*prompt = sb.String()
		return &openai.ChatCompletion{Choices: []openai.ChatCompletionChoice{{Message: openai.ChatCompletionMessage{Content: "ok"}}}}, nil
	}
	return prompt
}

func lazyTestDescription(t *testing.T, m *Module, id string) (string, int) {
	t.Helper()
	var descs []string
	if err := m.db.Model(&ChatAttachment{}).Where("message_id = ?", id).Pluck("image_description", &descs).Error; err != nil {
		t.Fatalf("query attachments: %v", err)
	}
	if len(descs) == 0 {
		return "", 0
	}
	return descs[0], len(descs)
}

func TestLazy_UnmentionedImageStoresPendingRow(t *testing.T) {
	m, session := setupGippityTest(t)
	m.describeImagesFunc = func([]string) (string, error) {
		t.Fatal("describe must not run for an unanswered post")
		return "", nil
	}
	lazyTestImagePost(t, m, session, "img-1", "poster", "https://cdn/a.png")

	var url, desc string
	if err := queryRow(t, m.db, "SELECT attachment_url, image_description FROM gippity_chat_attachments WHERE message_id = ?", "img-1").Scan(&url, &desc); err != nil {
		t.Fatalf("pending row missing: %v", err)
	}
	if url != "https://cdn/a.png" || desc != "" {
		t.Errorf("row = (%q, %q), want stored URL and empty description", url, desc)
	}
}

func TestLazy_HistoryImageDescribedAndCached(t *testing.T) {
	m, session := setupGippityTest(t)
	if err := m.setUserPrivacy("poster", false); err != nil {
		t.Fatal(err)
	}
	lazyTestImagePost(t, m, session, "img-1", "poster", "https://cdn/a.png")

	calls := 0
	m.describeImagesFunc = func([]string) (string, error) {
		calls++
		return "a red bicycle", nil
	}
	prompt := lazyTestCapturePrompt(m)

	if _, err := m.generateAnswer(lazyTestQuestion("q-1", ""), nil); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("describe calls = %d, want 1", calls)
	}
	if !strings.Contains(*prompt, "[Image 1: a red bicycle]") {
		t.Errorf("prompt lacks description:\n%s", *prompt)
	}
	if desc, n := lazyTestDescription(t, m, "img-1"); desc != "a red bicycle" || n != 1 {
		t.Errorf("stored = (%q, %d rows), want description in one row", desc, n)
	}

	if _, err := m.generateAnswer(lazyTestQuestion("q-2", ""), nil); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("cached description re-described: calls = %d", calls)
	}
	if !strings.Contains(*prompt, "[Image 1: a red bicycle]") {
		t.Errorf("cached description missing from prompt")
	}
}

func TestLazy_PrivateAuthorNeverDescribed(t *testing.T) {
	m, session := setupGippityTest(t)
	lazyTestImagePost(t, m, session, "img-1", "private-poster", "https://cdn/a.png")
	m.describeImagesFunc = func([]string) (string, error) {
		t.Fatal("describe must not run for a private author")
		return "", nil
	}
	prompt := lazyTestCapturePrompt(m)

	if _, err := m.generateAnswer(lazyTestQuestion("q-1", "img-1"), nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(*prompt, "[Image") {
		t.Errorf("prompt contains image data of a private author:\n%s", *prompt)
	}
}

func TestLazy_ReplyTargetOutsideWindowDescribed(t *testing.T) {
	m, session := setupGippityTest(t)
	if err := m.setUserPrivacy("poster", false); err != nil {
		t.Fatal(err)
	}
	lazyTestImagePost(t, m, session, "img-1", "poster", "https://cdn/a.png")
	for i := range 12 {
		if _, err := execSQL(t, m.db, `INSERT INTO gippity_chat_history (user_id, channel_id, timestamp, message, message_id, guild_id) VALUES (?, ?, ?, ?, ?, ?)`,
			"other", "channel-1", 2000000000+i, "chatter", fmt.Sprintf("filler-%d", i), "allowed-guild"); err != nil {
			t.Fatal(err)
		}
	}
	m.describeImagesFunc = func([]string) (string, error) { return "a sunset", nil }
	prompt := lazyTestCapturePrompt(m)

	if _, err := m.generateAnswer(lazyTestQuestion("q-1", "img-1"), nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(*prompt, "[System note: User is replying to a message from ") || !strings.Contains(*prompt, ": look [Image 1: a sunset]]") {
		t.Errorf("reply note lacks description:\n%s", *prompt)
	}
}

func TestLazy_CapPrefersReplyTarget(t *testing.T) {
	m, session := setupGippityTest(t)
	if err := m.setUserPrivacy("poster", false); err != nil {
		t.Fatal(err)
	}
	for i := range 5 {
		lazyTestImagePost(t, m, session, fmt.Sprintf("img-%d", i), "poster", fmt.Sprintf("https://cdn/%d.png", i))
		if _, err := execSQL(t, m.db, "UPDATE gippity_chat_history SET timestamp = ? WHERE message_id = ?", 1000+i, fmt.Sprintf("img-%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	var described []string
	m.describeImagesFunc = func(urls []string) (string, error) {
		described = append(described, urls[0])
		return "desc", nil
	}
	lazyTestCapturePrompt(m)

	if _, err := m.generateAnswer(lazyTestQuestion("q-1", "img-0"), nil); err != nil {
		t.Fatal(err)
	}
	want := []string{"https://cdn/0.png", "https://cdn/4.png", "https://cdn/3.png"}
	if strings.Join(described, " ") != strings.Join(want, " ") {
		t.Errorf("described = %v, want %v", described, want)
	}
}

func TestLazy_DescribeErrorStillAnswers(t *testing.T) {
	m, session := setupGippityTest(t)
	if err := m.setUserPrivacy("poster", false); err != nil {
		t.Fatal(err)
	}
	lazyTestImagePost(t, m, session, "img-1", "poster", "https://cdn/a.png")
	m.describeImagesFunc = func([]string) (string, error) { return "", errors.New("vision down") }
	lazyTestCapturePrompt(m)

	answer, err := m.generateAnswer(lazyTestQuestion("q-1", ""), nil)
	if err != nil || answer != "ok" {
		t.Fatalf("answer = %q, err = %v", answer, err)
	}
	if desc, n := lazyTestDescription(t, m, "img-1"); desc != "" || n != 1 {
		t.Errorf("stored = (%q, %d), want empty pending row", desc, n)
	}
}

func TestLazy_RefreshedURLUsedWithFallback(t *testing.T) {
	m, session := setupGippityTest(t)
	if err := m.setUserPrivacy("poster", false); err != nil {
		t.Fatal(err)
	}
	lazyTestImagePost(t, m, session, "img-1", "poster", "https://cdn/old.png")
	lazyTestImagePost(t, m, session, "img-2", "poster", "https://cdn/stored.png")
	m.channelMessageFunc = func(_ *discordgo.Session, _, id string) (*discordgo.Message, error) {
		if id == "img-1" {
			return &discordgo.Message{Attachments: []*discordgo.MessageAttachment{{Filename: "a.png", URL: "https://cdn/fresh.png"}}}, nil
		}
		return nil, errors.New("fetch failed")
	}
	got := map[string]bool{}
	m.describeImagesFunc = func(urls []string) (string, error) {
		got[urls[0]] = true
		return "x", nil
	}
	lazyTestCapturePrompt(m)

	if _, err := m.generateAnswer(lazyTestQuestion("q-1", ""), nil); err != nil {
		t.Fatal(err)
	}
	if !got["https://cdn/fresh.png"] || !got["https://cdn/stored.png"] || got["https://cdn/old.png"] {
		t.Errorf("described URLs = %v", got)
	}
}

func TestLazy_EligibleImageMentionKeepsSingleRow(t *testing.T) {
	m, session := setupGippityTest(t)
	m.generateAnswerFunc = func(*discordgo.MessageCreate, []string) (string, error) { return "", nil }
	m.describeImagesFunc = func([]string) (string, error) { return "a cat", nil }
	msg := gippityTestMessageWithImage("what is this <@bot-user>", "https://cdn/a.png")
	msg.Mentions = []*discordgo.User{{ID: "bot-user"}}

	m.onMessageCreate(session, msg)

	if desc, n := lazyTestDescription(t, m, msg.ID); desc != "a cat" || n != 1 {
		t.Errorf("stored = (%q, %d rows), want one described row", desc, n)
	}
}
