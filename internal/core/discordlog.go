package gidbig

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"runtime"
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

func logDiscordgo(level, caller int, format string, args ...interface{}) {
	// The fork logs entire READY payloads at info, including user/session data.
	// Keep the useful opcode/type/sequence without the multi-kilobyte dump.
	if strings.HasPrefix(format, "First Packet:") && len(args) == 1 {
		if event, ok := args[0].(*discordgo.Event); ok && event != nil {
			format = "Gateway handshake packet: op=%d type=%s sequence=%d"
			args = []interface{}{event.Operation, event.Type, event.Sequence}
		}
	}
	message := strings.TrimSpace(fmt.Sprintf(format, args...))
	attrs := []any{"component", "discordgo", "discordgo_level", discordgoLevelName(level)}
	// discordgo passes the depth relative to msglog; our callback adds a frame.
	if pc, file, line, ok := runtime.Caller(caller + 1); ok {
		attrs = append(attrs, "discordgo_source", fmt.Sprintf("%s:%d", filepath.Base(file), line))
		if fn := runtime.FuncForPC(pc); fn != nil {
			attrs = append(attrs, "discordgo_function", fn.Name())
		}
	}

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
