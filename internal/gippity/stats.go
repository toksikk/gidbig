package gippity

import (
	"context"
	"errors"
	"strconv"

	"github.com/toksikk/gidbig/internal/bot"
)

// Stats reports stored chat counters for /status.
func (m *Module) Stats(ctx context.Context) (bot.ModuleStats, error) {
	if m.db == nil {
		return bot.ModuleStats{}, errors.New("database not open")
	}
	var messages, mentions, users, channels, edits, images, privacyOff int64
	queries := []struct {
		dst   *int64
		query string
	}{
		{&messages, "SELECT COUNT(*) FROM chat_history"},
		{&mentions, "SELECT COUNT(*) FROM chat_history WHERE is_bot_mention = 1"},
		{&users, "SELECT COUNT(DISTINCT user_id) FROM chat_history"},
		{&channels, "SELECT COUNT(DISTINCT channel_id) FROM chat_history"},
		{&edits, "SELECT COUNT(*) FROM chat_history_edits"},
		{&images, "SELECT COUNT(*) FROM chat_attachments"},
		{&privacyOff, "SELECT COUNT(*) FROM user_privacy WHERE privacy_enabled = 0"},
	}
	for _, q := range queries {
		if err := m.db.QueryRowContext(ctx, q.query).Scan(q.dst); err != nil {
			return bot.ModuleStats{}, err
		}
	}

	return bot.ModuleStats{
		Summary: []bot.Stat{
			{Name: "msgs", Value: strconv.FormatInt(messages, 10)},
			{Name: "mentions", Value: strconv.FormatInt(mentions, 10)},
			{Name: "users", Value: strconv.FormatInt(users, 10)},
		},
		Detail: []bot.Stat{
			{Name: "channels", Value: strconv.FormatInt(channels, 10)},
			{Name: "edits", Value: strconv.FormatInt(edits, 10)},
			{Name: "images", Value: strconv.FormatInt(images, 10)},
			{Name: "privacy off", Value: strconv.FormatInt(privacyOff, 10)},
			{Name: "rate limit", Value: strconv.Itoa(m.userMessageLimit) + "/h"},
		},
	}, nil
}
