package gidbig

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
)

func TestConfigureDiscordgoLogging(t *testing.T) {
	tests := []struct {
		name    string
		devMode bool
		want    int
	}{
		{name: "production logs reconnect lifecycle", want: discordgo.LogInformational},
		{name: "development logs debug details", devMode: true, want: discordgo.LogDebug},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := discordgo.New("Bot fake-token")
			if err != nil {
				t.Fatalf("discordgo.New: %v", err)
			}

			configureDiscordgoLogging(s, tt.devMode)
			if s.LogLevel != tt.want {
				t.Errorf("LogLevel = %d, want %d", s.LogLevel, tt.want)
			}
			if discordgo.Logger == nil {
				t.Error("discordgo.Logger is nil")
			}
		})
	}
}

func TestLogDiscordgoUsesStructuredLogger(t *testing.T) {
	var output bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previousLogger) })

	logDiscordgo(discordgo.LogWarning, 0, " reconnect failed: %s\n", "timeout")

	got := output.String()
	for _, want := range []string{
		`"level":"WARN"`,
		`"msg":"reconnect failed: timeout"`,
		`"component":"discordgo"`,
		`"discordgo_level":"warning"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("log output %q does not contain %q", got, want)
		}
	}
}

func TestDiscordgoLevelNameUnknown(t *testing.T) {
	if got := discordgoLevelName(99); got != "unknown" {
		t.Fatalf("discordgoLevelName(99) = %q, want unknown", got)
	}
}
