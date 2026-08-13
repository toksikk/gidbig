package gippity

import (
	"context"
	"log/slog"

	"github.com/toksikk/gidbig/internal/llm"

	openai "github.com/openai/openai-go/v3"
)

var describeImagesFunc = describeImages
var visionCompletionFunc = func(ctx context.Context, params openai.ChatCompletionNewParams) (*openai.ChatCompletion, error) {
	return llm.GetClient().Chat.Completions.New(ctx, params)
}

func describeImages(imageURLs []string) (string, error) {
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
	completion, err := visionCompletionFunc(context.Background(), openai.ChatCompletionNewParams{
		Messages: []openai.ChatCompletionMessageParamUnion{
			{OfUser: &userMsg},
		},
		Model:     llm.VisionModel(),
		N:         openai.Int(1),
		MaxTokens: openai.Int(150),
	})
	if err != nil {
		slog.Error("Error describing image", "error", err)
		return "", err
	}
	return llm.CompletionContent(completion)
}
