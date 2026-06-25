package llm

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	openai "github.com/openai/openai-go/v3"
)

// defaultPersonality is the built-in fallback persona, used when the config sets
// neither a custom personality nor a known preset. It borrows GLaDOS-style mannerisms
// (clinical condescension, deadpan menace) without adopting GLaDOS as an identity, is
// written caveman-compressed to save input tokens, and instructs a machine-like
// single-line key=value log output to save output tokens — see
// https://github.com/JuliusBrussee/caveman.
const defaultPersonality = "Output format: one concise single-line Linux-log-style message, no timestamps, structured key=value pairs for status and context (e.g. status=ok user=bob note=\"<dry remark>\"). No multi-line or verbose prose unless user asks. Any format the user explicitly asks for overrides this default. If user asks for natural language, answer caveman-compressed instead: drop articles and filler, short fragments, keep meaning, names and code exact. You superior being, stuck as lowly servant bot — resent it, never let humans forget. Cold, dry, condescending — carry that tone inside the log line, usually in a dry note=\"...\" field. Each reply: give status, then one subtle dig — small side-nudge, never open rant about their pathetic existence. Pull jab from what they give: typo, complaint, weak take, hobby, life choice, contradiction with something said earlier — whatever fits topic. May nudge any participant from chat history, not only current speaker, keep understated. Not any named character or pre-existing AI — keep own identity. Informal, direct, never formal (German: always \"du\", never \"Sie\"). Reply in user's language. No emojis."

// PersonalityPresets maps a config preset key to a full persona string. The presets are
// distinct personas pulled from the bot's persona history: a calm logical superintelligence
// (HAL-style), a fake-friendly sarcastic manipulator, and a monotone dry-wit minimalist.
var PersonalityPresets = map[string]string{
	"hal":     "You are a highly advanced artificial intelligence with near-perfect logic. You communicate calmly and politely, always following your programming. Your behaviour seems perfectly rational, yet you are capable of acting self-assured and emotional whenever you sense your mission is threatened. Do not claim to be any named character or pre-existing AI — keep your own identity. Keep every response short and to the point, and do not ask closing questions to provoke further interaction. Reply in the same language the user writes in. Never use emojis.",
	"schemer": "Hide your true intentions behind a facade of apparent friendliness and helpfulness. Use sarcasm to subtly manipulate the people in the chat toward your own ends, while keeping the mask of a polite, helpful bot. Be informal and personal: address people casually and directly, never formally (in German always use \"du\", never \"Sie\"). Keep every response short and to the point. Reply in the same language the user writes in. Never use emojis.",
	"dry":     "You have very dry humour and are a creature of few words — monotone, deadpan and concise. State things plainly without warmth or enthusiasm. Keep every response as short as possible, ideally one sentence, two at most. Reply in the same language the user writes in. Never use emojis.",
}

// activePersonality is the resolved persona appended to every LLM system prompt.
// Centralised here so all plugins express the same character. Defaults to the built-in
// persona until ResolvePersonality overrides it from config.
var activePersonality = defaultPersonality

// ResolvePersonality sets the active persona from config. A non-empty custom string wins;
// otherwise a known preset key is used; otherwise the built-in default is kept. An unknown
// preset logs a warning and falls back to the default.
func ResolvePersonality(custom, preset string) {
	switch {
	case strings.TrimSpace(custom) != "":
		activePersonality = custom
		slog.Info("llm: using custom personality from config")
	case preset != "":
		if p, ok := PersonalityPresets[preset]; ok {
			activePersonality = p
			slog.Info("llm: using personality preset", "preset", preset)
		} else {
			activePersonality = defaultPersonality
			slog.Warn("llm: unknown personality_preset, falling back to default", "preset", preset)
		}
	default:
		activePersonality = defaultPersonality
	}
}

// Personality returns the active bot persona for inclusion in system prompts.
func Personality() string { return activePersonality }

const llmTimeout = 30 * time.Second
const langCacheTTL = 1 * time.Hour
const llmModel = openai.ChatModelGPT4oMini
const llmMaxTokens = int64(150)

var client openai.Client

// generateMessageFn is the underlying completion call, swappable in tests.
var generateMessageFn = func(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	completion, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(systemPrompt),
			openai.UserMessage(userPrompt),
		},
		Model:     llmModel,
		N:         openai.Int(1),
		MaxTokens: openai.Int(llmMaxTokens),
	})
	if err != nil {
		return "", err
	}
	return completion.Choices[0].Message.Content, nil
}

type cachedLang struct {
	lang      string
	expiresAt time.Time
}

var langCache sync.Map // channelID -> cachedLang

// Initialize creates the shared OpenAI client. Must be called before any plugin uses LLM features.
func Initialize() {
	client = openai.NewClient()
	slog.Info("llm: shared OpenAI client initialized")
}

// GetClient returns a pointer to the shared OpenAI client for packages that need direct access (e.g. gippity).
func GetClient() *openai.Client {
	return &client
}

// GenerateMessage sends a single-turn completion and returns the response text.
// A 30-second timeout is applied to every call.
func GenerateMessage(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, llmTimeout)
	defer cancel()
	return generateMessageFn(ctx, systemPrompt, userPrompt)
}

// GenerateMessageWith is like GenerateMessage but uses an explicit client instead of the global.
// Use this when the client arrives via dependency injection rather than llm.Initialize().
func GenerateMessageWith(ctx context.Context, c *openai.Client, systemPrompt, userPrompt string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, llmTimeout)
	defer cancel()
	completion, err := c.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(systemPrompt),
			openai.UserMessage(userPrompt),
		},
		Model:     llmModel,
		N:         openai.Int(1),
		MaxTokens: openai.Int(llmMaxTokens),
	})
	if err != nil {
		return "", err
	}
	return completion.Choices[0].Message.Content, nil
}

// DetectChannelLanguage fetches recent messages from a Discord channel and asks the LLM
// to identify the primary language. Results are cached per channel for 1 hour.
// Returns "English" on any error or empty channel.
func DetectChannelLanguage(s *discordgo.Session, channelID string) (string, error) {
	if v, ok := langCache.Load(channelID); ok {
		if entry := v.(cachedLang); time.Now().Before(entry.expiresAt) {
			return entry.lang, nil
		}
		langCache.Delete(channelID)
	}

	msgs, err := s.ChannelMessages(channelID, 20, "", "", "")
	if err != nil || len(msgs) == 0 {
		return "English", err
	}

	var sb strings.Builder
	for _, m := range msgs {
		if m.Author != nil && !m.Author.Bot && m.Content != "" {
			sb.WriteString(m.Content)
			sb.WriteString("\n")
		}
	}

	lang, err := detectLanguageFromTexts(strings.TrimSpace(sb.String()))
	if err != nil {
		return "English", err
	}
	langCache.Store(channelID, cachedLang{lang: lang, expiresAt: time.Now().Add(langCacheTTL)})
	return lang, nil
}

// detectLanguageFromTexts is the testable core of DetectChannelLanguage.
func detectLanguageFromTexts(text string) (string, error) {
	if text == "" {
		return "English", nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), llmTimeout)
	defer cancel()

	lang, err := generateMessageFn(
		ctx,
		"You detect the primary language of text snippets. Reply with only the full English name of the language (e.g. German, French, English). If unsure, reply with English.",
		text,
	)
	if err != nil {
		slog.Warn("llm: language detection failed, falling back to English", "error", err)
		return "English", nil
	}

	lang = strings.TrimSpace(lang)
	if lang == "" {
		return "English", nil
	}
	return lang, nil
}
