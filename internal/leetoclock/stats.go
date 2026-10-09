package leetoclock

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/toksikk/gidbig/internal/bot"
)

// Stats reports leetoclock counters for /status.
func (m *Module) Stats(ctx context.Context) (bot.ModuleStats, error) {
	if m.store == nil {
		return bot.ModuleStats{}, errors.New("store not open")
	}
	now := m.now()
	c, err := m.store.Counts(ctx, now)
	if err != nil {
		return bot.ModuleStats{}, err
	}
	m.stateMu.RLock()
	target := fmt.Sprintf("%02d:%02d", m.targetHour, m.targetMinute)
	channels := len(m.announcementChannels)
	m.stateMu.RUnlock()

	return bot.ModuleStats{
		Summary: []bot.Stat{
			{Name: "scores", Value: strconv.FormatInt(c.Scores, 10)},
			{Name: "games", Value: strconv.FormatInt(c.Games, 10)},
			{Name: "season " + now.Format("2006-01"), Value: fmt.Sprintf("%d games/%d players", c.SeasonGames, c.SeasonPlayers)},
		},
		Detail: []bot.Stat{
			{Name: "players", Value: strconv.FormatInt(c.Players, 10)},
			{Name: "seasons", Value: strconv.FormatInt(c.Seasons, 10)},
			{Name: "target", Value: target},
			{Name: "announce channels", Value: strconv.Itoa(channels)},
		},
	}, nil
}
