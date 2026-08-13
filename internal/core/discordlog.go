package gidbig

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/bwmarrin/discordgo"
)

// configureDiscordgoLogging sends the library's gateway and voice diagnostics
// through the application's structured logger. Informational logging includes
// reconnect attempts and handshake progress; debug additionally includes event
// payloads and heartbeat traffic.
func configureDiscordgoLogging(s *discordgo.Session, devMode bool) {
	discordgo.Logger = logDiscordgo
	s.LogLevel = discordgo.LogInformational
	if devMode {
		s.LogLevel = discordgo.LogDebug
	}
}

func logDiscordgo(level, _ int, format string, args ...interface{}) {
	message := strings.TrimSpace(fmt.Sprintf(format, args...))
	attrs := []any{"component", "discordgo", "discordgo_level", discordgoLevelName(level)}

	switch level {
	case discordgo.LogError:
		slog.Error(message, attrs...)
	case discordgo.LogWarning:
		slog.Warn(message, attrs...)
	case discordgo.LogInformational:
		slog.Info(message, attrs...)
	default:
		slog.Debug(message, attrs...)
	}
}

func discordgoLevelName(level int) string {
	switch level {
	case discordgo.LogError:
		return "error"
	case discordgo.LogWarning:
		return "warning"
	case discordgo.LogInformational:
		return "info"
	case discordgo.LogDebug:
		return "debug"
	default:
		return "unknown"
	}
}
