package gbpgippity

import (
	"context"
	"log/slog"

	openai "github.com/openai/openai-go"
)

// nolint: unused
func createNewLongtermMemory() {
	allBotNames := getBotDisplayNames()
	// TODO: write a string for chatCompletion in human language that describes all bot names and their respective guilds
	botNames := ""
	for guildID, botName := range allBotNames {
		botNames += botName + " in " + guildID + ". "
	}

	messageForLongterm, err := getOldestChatMessageAsJSON(chatHistory)
	if err != nil {
		slog.Warn("Error while getting oldest chat message", "error", err)
		return
	}

	systemMessage := `
	Du bist ein Discord Chatbot in einem Channel mit vielen verschiedenen Nutzern, auf mehreren Servern (auch Gilden genannt) und jeweils mit mehreren Textkanälen.
	Du kannst auf Servern unterschiedliche Namen haben.
	Deine Namen auf den jeweiligen Servern sind: ` + botNames + `.
	Du erhältst eine neue Nachricht im JSON Format und deine Aufgabe ist eine bereits bestehende Zusammenfassung der Vergangenheit mit den wichtigsten Informationen zu erweitern.
	Die Zusammenfassung soll maximal 2000 Wörter haben. Du hast alle Informationen, die du benötigst.
	Stelle keine Rückfragen.
	Schreibe ausschließlich die Zusammenfassung und füge keine Kommentare hinzu.
	Du musst nicht sagen, dass etwas erweitert wurde.
	`

	userMessage := `
	Die bisherige Zusammenfassung: ` + chatHistory.LongtermMemory + `
	Die neue Nachricht, die du der bisherigen Zusammenfassung ergänzen sollst: ` + messageForLongterm + `
	`

	chatCompletion, err := openaiClient.Chat.Completions.New(context.TODO(), openai.ChatCompletionNewParams{
		Messages: openai.F([]openai.ChatCompletionMessageParamUnion{
			openai.ChatCompletionMessageParamUnion(openai.SystemMessage(systemMessage)),
			openai.ChatCompletionMessageParamUnion(openai.UserMessage(userMessage)),
		}),
		Model: openai.F(openai.ChatModelGPT4oMini),
		N:     openai.Int(1),
	})

	if err != nil {
		slog.Info("Error while getting completion", "error", err)
		return
	}

	slog.Info("Chat completion", "completion", chatCompletion)

	if len(chatCompletion.Choices) == 0 {
		slog.Info("No completion choices found")
		return
	}

	chatHistory.LongtermMemory = chatCompletion.Choices[0].Message.Content
}
