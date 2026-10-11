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
	db := m.db.WithContext(ctx)

	var messages, mentions, users, channels, edits, images, privacyOff int64
	queries := []struct {
		dst *int64
		run func() error
	}{
		{&messages, func() error { return db.Model(&ChatMessage{}).Count(&messages).Error }},
		{&mentions, func() error {
			return db.Model(&ChatMessage{}).Where("is_bot_mention = ?", true).Count(&mentions).Error
		}},
		{&users, func() error {
			return db.Model(&ChatMessage{}).Distinct("user_id").Count(&users).Error
		}},
		{&channels, func() error {
			return db.Model(&ChatMessage{}).Distinct("channel_id").Count(&channels).Error
		}},
		{&edits, func() error { return db.Model(&ChatHistoryEdit{}).Count(&edits).Error }},
		{&images, func() error { return db.Model(&ChatAttachment{}).Count(&images).Error }},
		{&privacyOff, func() error {
			return db.Model(&UserPrivacy{}).Where("privacy_enabled = ?", false).Count(&privacyOff).Error
		}},
	}
	for _, q := range queries {
		if err := q.run(); err != nil {
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
