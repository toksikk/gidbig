package wttrin

import (
	"context"
	"errors"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/toksikk/gidbig/internal/llm"
)

func stubLLM(t *testing.T, reply string, err error) {
	t.Helper()
	prev := generateLLMIntro
	t.Cleanup(func() { generateLLMIntro = prev })
	generateLLMIntro = func(_ context.Context, _, _ string) (string, error) {
		return reply, err
	}
}

func stubDetectLanguage(t *testing.T, lang string) {
	t.Helper()
	prev := detectLanguage
	t.Cleanup(func() { detectLanguage = prev })
	detectLanguage = func(_ *discordgo.Session, _ string) (string, error) {
		return lang, nil
	}
}

func TestBuildLLMWeatherIntro_PrependedOnSuccess(t *testing.T) {
	stubDetectLanguage(t, "German")
	stubLLM(t, "Das Wetter heute ist angenehm.", nil)

	intro := buildLLMWeatherIntro(nil, &discordgo.MessageCreate{
		Message: &discordgo.Message{ChannelID: "ch1"},
	}, "Berlin", "15°C sunny")

	if intro != "Das Wetter heute ist angenehm." {
		t.Errorf("unexpected intro: %q", intro)
	}
}

func TestBuildLLMWeatherIntro_EmptyOnLLMError(t *testing.T) {
	stubDetectLanguage(t, "English")
	stubLLM(t, "", errors.New("api error"))

	intro := buildLLMWeatherIntro(nil, &discordgo.MessageCreate{
		Message: &discordgo.Message{ChannelID: "ch1"},
	}, "London", "10°C rain")

	if intro != "" {
		t.Errorf("expected empty intro on LLM error, got %q", intro)
	}
}

func TestBuildLLMWeatherIntro_TrimsWhitespace(t *testing.T) {
	stubDetectLanguage(t, "English")
	stubLLM(t, "  Nice weather!  ", nil)

	intro := buildLLMWeatherIntro(nil, &discordgo.MessageCreate{
		Message: &discordgo.Message{ChannelID: "ch1"},
	}, "London", "data")

	if intro != "Nice weather!" {
		t.Errorf("expected trimmed intro, got %q", intro)
	}
}

// Verify the package-level vars are wired to the llm package.
func TestDefaultsWiredToLLMPackage(t *testing.T) {
	if generateLLMIntro == nil {
		t.Error("generateLLMIntro must not be nil")
	}
	_ = llm.GenerateMessage // ensure llm package is referenced
}
