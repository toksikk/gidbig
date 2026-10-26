package trivia

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Stat keys stored in trivia_daily_counts.stat.
const (
	statMessages          = "messages"
	statWords             = "words"
	statGIFs              = "gifs"
	statPictures          = "pictures"
	statLinks             = "links"
	statReactionsGiven    = "reactions_given"
	statReactionsReceived = "reactions_received"
)

// allStats lists every stat a drop-in can pick.
var allStats = []string{statMessages, statWords, statGIFs, statPictures, statLinks, statReactionsGiven, statReactionsReceived}

// windowDays is the length of the rolling window, and of the author index.
const windowDays = 30

// minParticipants is the fewest consenting users with a non-zero count a
// stat needs before a drop-in may name a leader.
const minParticipants = 3

const dayLayout = "2006-01-02"

// DailyCount is one user's counter for one stat on one UTC day in one guild.
type DailyCount struct {
	GuildID string `gorm:"primaryKey"`
	UserID  string `gorm:"primaryKey"`
	Day     string `gorm:"primaryKey"`
	Stat    string `gorm:"primaryKey"`
	Count   int64  `gorm:"not null;default:0"`
}

// TableName pins the table name.
func (DailyCount) TableName() string { return "trivia_daily_counts" }

// MessageAuthor maps a counted message to its author so reactions on it can
// be credited as received. Rows older than windowDays are pruned.
type MessageAuthor struct {
	MessageID string `gorm:"primaryKey"`
	GuildID   string
	AuthorID  string
	CreatedAt time.Time `gorm:"index"`
}

// TableName pins the table name.
func (MessageAuthor) TableName() string { return "trivia_message_authors" }

// GuildState persists the per-guild drop-in cooldown across restarts.
type GuildState struct {
	GuildID      string `gorm:"primaryKey"`
	LastDropinAt time.Time
}

// TableName pins the table name.
func (GuildState) TableName() string { return "trivia_guild_state" }

// store wraps the shared database. It never closes it.
type store struct {
	db *gorm.DB
}

func newStore(db *gorm.DB) (*store, error) {
	if db == nil {
		return nil, errors.New("database is nil")
	}
	if err := db.AutoMigrate(&DailyCount{}, &MessageAuthor{}, &GuildState{}); err != nil {
		return nil, fmt.Errorf("auto-migrate: %w", err)
	}
	return &store{db: db}, nil
}

func dayOf(t time.Time) string { return t.UTC().Format(dayLayout) }

// add changes a counter by delta. A negative delta never takes the counter
// below zero.
func (s *store) add(ctx context.Context, guildID, userID string, at time.Time, stat string, delta int64) error {
	if delta == 0 {
		return nil
	}
	day := dayOf(at)
	if delta > 0 {
		row := DailyCount{GuildID: guildID, UserID: userID, Day: day, Stat: stat, Count: delta}
		err := s.db.WithContext(ctx).Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "guild_id"}, {Name: "user_id"}, {Name: "day"}, {Name: "stat"}},
			DoUpdates: clause.Assignments(map[string]any{"count": gorm.Expr("trivia_daily_counts.count + excluded.count")}),
		}).Create(&row).Error
		if err != nil {
			return fmt.Errorf("increment %s: %w", stat, err)
		}
		return nil
	}
	err := s.db.WithContext(ctx).Model(&DailyCount{}).
		Where("guild_id = ? AND user_id = ? AND day = ? AND stat = ?", guildID, userID, day, stat).
		Update("count", gorm.Expr("MAX(count + ?, 0)", delta)).Error
	if err != nil {
		return fmt.Errorf("decrement %s: %w", stat, err)
	}
	return nil
}

// leader is one user's total for a stat.
type leader struct {
	UserID string
	Total  int64
}

// leaderFor returns the consenting user with the highest total for stat in
// guildID, counting days on or after since (empty = all time). ok is false
// when fewer than minParticipants consenting users have a non-zero total.
// Ties go to the lowest user ID so the result is stable.
func (s *store) leaderFor(ctx context.Context, guildID, stat, since string, counted func(string) bool) (leader, bool, error) {
	q := s.db.WithContext(ctx).Model(&DailyCount{}).
		Select("user_id, SUM(count) AS total").
		Where("guild_id = ? AND stat = ?", guildID, stat)
	if since != "" {
		q = q.Where("day >= ?", since)
	}
	var rows []leader
	err := q.Group("user_id").Having("SUM(count) > 0").
		Order("total DESC, user_id ASC").Scan(&rows).Error
	if err != nil {
		return leader{}, false, fmt.Errorf("query leaders for %s: %w", stat, err)
	}
	var eligible []leader
	for _, r := range rows {
		if counted(r.UserID) {
			eligible = append(eligible, r)
		}
	}
	if len(eligible) < minParticipants {
		return leader{}, false, nil
	}
	return eligible[0], true, nil
}

// indexAuthor records the author of a counted message.
func (s *store) indexAuthor(ctx context.Context, messageID, guildID, authorID string, at time.Time) error {
	row := MessageAuthor{MessageID: messageID, GuildID: guildID, AuthorID: authorID, CreatedAt: at.UTC()}
	if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
		return fmt.Errorf("index message author: %w", err)
	}
	return nil
}

// authorOf returns the indexed author of messageID, or "" when unknown.
func (s *store) authorOf(ctx context.Context, messageID string) (string, error) {
	var row MessageAuthor
	err := s.db.WithContext(ctx).Where("message_id = ?", messageID).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("look up message author: %w", err)
	}
	return row.AuthorID, nil
}

// pruneAuthors deletes author index rows created before cutoff.
func (s *store) pruneAuthors(ctx context.Context, cutoff time.Time) (int64, error) {
	res := s.db.WithContext(ctx).Where("created_at < ?", cutoff.UTC()).Delete(&MessageAuthor{})
	if res.Error != nil {
		return 0, fmt.Errorf("prune message authors: %w", res.Error)
	}
	return res.RowsAffected, nil
}

// lastDropin returns the persisted last drop-in time for guildID (zero if none).
func (s *store) lastDropin(ctx context.Context, guildID string) (time.Time, error) {
	var row GuildState
	err := s.db.WithContext(ctx).Where("guild_id = ?", guildID).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("load guild state: %w", err)
	}
	return row.LastDropinAt, nil
}

// setLastDropin persists the last drop-in time for guildID.
func (s *store) setLastDropin(ctx context.Context, guildID string, at time.Time) error {
	row := GuildState{GuildID: guildID, LastDropinAt: at.UTC()}
	err := s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "guild_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"last_dropin_at"}),
	}).Create(&row).Error
	if err != nil {
		return fmt.Errorf("save guild state: %w", err)
	}
	return nil
}
