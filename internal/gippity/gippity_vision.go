package gippity

import (
	"context"
	"log/slog"

	"github.com/toksikk/gidbig/internal/llm"

	openai "github.com/openai/openai-go/v3"
)

func (m *Module) describeImages(imageURLs []string) (string, error) {
	parts := []openai.ChatCompletionContentPartUnionParam{
		openai.TextContentPart("Describe what is in this image concisely."),
	}
	for _, url := range imageURLs {
		parts = append(parts, openai.ImageContentPart(openai.ChatCompletionContentPartImageImageURLParam{URL: url}))
	}
	userMsg := openai.ChatCompletionUserMessageParam{
		Content: openai.ChatCompletionUserMessageParamContentUnion{
			OfArrayOfContentParts: parts,
		},
	}
	completion, err := m.visionCompletionFunc(context.Background(), openai.ChatCompletionNewParams{
		Messages: []openai.ChatCompletionMessageParamUnion{
			{OfUser: &userMsg},
		},
		Model:               llm.VisionModel(),
		N:                   openai.Int(1),
		MaxCompletionTokens: openai.Int(150),
	})
	if err != nil {
		slog.Error("Error describing image", "error", err)
		return "", err
	}
	return llm.CompletionContent(completion)
}
