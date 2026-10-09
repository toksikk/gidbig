package datastore

import (
	"context"
	"time"
)

// Counts summarizes stored rows for status reporting.
type Counts struct {
	Players       int64
	Games         int64
	Scores        int64
	Seasons       int64
	SeasonGames   int64
	SeasonPlayers int64
}

// Counts returns row counts overall and for the season containing now.
func (s *Store) Counts(ctx context.Context, now time.Time) (Counts, error) {
	db := s.db.WithContext(ctx)
	var c Counts
	start, end := getSeasonStartDateForDate(now), getSeasonEndDateForDate(now)
	queries := []func() error{
		func() error { return db.Model(&Player{}).Count(&c.Players).Error },
		func() error { return db.Model(&Game{}).Count(&c.Games).Error },
		func() error { return db.Model(&Score{}).Count(&c.Scores).Error },
		func() error { return db.Model(&Season{}).Count(&c.Seasons).Error },
		func() error {
			return db.Model(&Game{}).Where("game_date BETWEEN ? AND ?", start, end).Count(&c.SeasonGames).Error
		},
		func() error {
			return db.Model(&Score{}).
				Joins("JOIN leetoclock_games ON leetoclock_games.id = leetoclock_scores.game_id").
				Where("leetoclock_games.game_date BETWEEN ? AND ?", start, end).
				Distinct("leetoclock_scores.player_id").
				Count(&c.SeasonPlayers).Error
		},
	}
	for _, q := range queries {
		if err := q(); err != nil {
			return Counts{}, err
		}
	}
	return c, nil
}
