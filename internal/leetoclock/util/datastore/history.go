package datastore

import (
	"fmt"
	"sort"
	"time"
)

// Period selects a game-date window for score-history queries.
type Period string

const (
	PeriodMonth      Period = "month"
	PeriodToday      Period = "today"
	PeriodYesterday  Period = "yesterday"
	PeriodWeek       Period = "week"
	PeriodLast7Days  Period = "last7days"
	PeriodLast30Days Period = "last30days"
	PeriodYear       Period = "year"
	PeriodAllTime    Period = "alltime"
	maxHistoryRows          = 10
	// maxDailyRows caps how many day winners a daily query returns. The most
	// recent days win when a window holds more.
	maxDailyRows = 40
)

// ScoreRecord contains one valid attempt and its Discord message location.
type ScoreRecord struct {
	Score     int
	UserID    string
	GameDate  time.Time
	GuildID   string
	ChannelID string
	MessageID string
}

// historyBounds uses one caller-supplied instant in the bot's local timezone.
// The end is exclusive, including for all-time queries, so future games are excluded.
func historyBounds(period Period, now time.Time) (time.Time, time.Time, error) {
	now = now.In(time.Local)
	switch period {
	case "", PeriodMonth:
		return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local), now, nil
	case PeriodToday:
		return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local), now, nil
	case PeriodYesterday:
		today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
		return today.AddDate(0, 0, -1), today, nil
	case PeriodWeek:
		// Calendar week starting on Monday, from local midnight up to now.
		midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
		return midnight.AddDate(0, 0, -int((midnight.Weekday()+6)%7)), now, nil
	case PeriodLast7Days:
		return now.Add(-7 * 24 * time.Hour), now, nil
	case PeriodLast30Days:
		return now.Add(-30 * 24 * time.Hour), now, nil
	case PeriodYear:
		return time.Date(now.Year(), time.January, 1, 0, 0, 0, 0, time.Local), now, nil
	case PeriodAllTime:
		return time.Time{}, now, nil
	default:
		return time.Time{}, time.Time{}, fmt.Errorf("unknown score-history period %q", period)
	}
}

// historyQuery builds the common joins and filters before any ranking or limit.
// julianday compares instants even when stored game dates have different UTC offsets.
func historyQuery(start, end time.Time, guildID, userID string) (string, []any) {
	query := `FROM leetoclock_scores AS s
		JOIN leetoclock_games AS g ON g.id = s.game_id
		JOIN leetoclock_players AS p ON p.id = s.player_id
		WHERE s.deleted_at IS NULL AND g.deleted_at IS NULL AND p.deleted_at IS NULL
			AND s.score >= 0 AND julianday(g.game_date) < julianday(?)`
	args := []any{end}
	if !start.IsZero() {
		query += " AND julianday(g.game_date) >= julianday(?)"
		args = append(args, start)
	}
	if guildID != "" {
		query += " AND g.guild_id = ?"
		args = append(args, guildID)
	}
	if userID != "" {
		query += " AND p.user_id = ?"
		args = append(args, userID)
	}
	return query, args
}

// DayRange returns the half-open [start, end) window covering the inclusive
// local-day range from..to. Either side may be the zero time to leave that bound
// open. The end is exclusive, so a single day selects exactly that game date.
func DayRange(from, to time.Time) (time.Time, time.Time) {
	var start, end time.Time
	if !from.IsZero() {
		start = time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, time.Local)
	}
	if !to.IsZero() {
		end = time.Date(to.Year(), to.Month(), to.Day(), 0, 0, 0, 0, time.Local).AddDate(0, 0, 1)
	}
	return start, end
}

const recordColumns = `s.score, p.user_id, g.game_date, g.guild_id, g.channel_id, s.message_id`

// TopPlayers returns up to ten distinct players' best valid attempts.
// Empty guildID selects records across all guilds. Ties are resolved by score,
// game date, message ID, then user ID.
func (s *Store) TopPlayers(guildID string, period Period, now time.Time) ([]ScoreRecord, error) {
	start, end, err := historyBounds(period, now)
	if err != nil {
		return nil, err
	}
	return s.TopPlayersBetween(guildID, start, end)
}

// TopPlayersBetween is TopPlayers for an explicit half-open [start, end) window.
// It backs date-specific record lookups, which no Period can express.
func (s *Store) TopPlayersBetween(guildID string, start, end time.Time) ([]ScoreRecord, error) {
	from, args := historyQuery(start, end, guildID, "")
	query := `WITH ranked AS (
		SELECT ` + recordColumns + `,
			ROW_NUMBER() OVER (PARTITION BY s.player_id ORDER BY s.score, julianday(g.game_date), s.message_id, p.user_id) AS rank
		` + from + `
	)
	SELECT score, user_id, game_date, guild_id, channel_id, message_id
	FROM ranked WHERE rank = 1
	ORDER BY score, julianday(game_date), message_id, user_id LIMIT ?`
	s.mu.Lock()
	defer s.mu.Unlock()
	records := make([]ScoreRecord, 0)
	err := s.db.Raw(query, append(args, maxHistoryRows)...).Scan(&records).Error
	return records, err
}

// PlayerRecords returns up to ten best valid attempts and the full valid attempt
// count for one user. Empty guildID selects that user's records across all guilds.
func (s *Store) PlayerRecords(userID, guildID string, period Period, now time.Time) ([]ScoreRecord, int64, error) {
	start, end, err := historyBounds(period, now)
	if err != nil {
		return nil, 0, err
	}
	return s.PlayerRecordsBetween(userID, guildID, start, end)
}

// PlayerRecordsBetween is PlayerRecords for an explicit half-open [start, end)
// window.
func (s *Store) PlayerRecordsBetween(userID, guildID string, start, end time.Time) ([]ScoreRecord, int64, error) {
	if userID == "" {
		return nil, 0, fmt.Errorf("user ID required for player records")
	}
	from, args := historyQuery(start, end, guildID, userID)
	query := `SELECT ` + recordColumns + `, COUNT(*) OVER () AS total
		` + from + `
		ORDER BY s.score, julianday(g.game_date), s.message_id LIMIT ?`
	var rows []struct {
		ScoreRecord
		Total int64
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.db.Raw(query, append(args, maxHistoryRows)...).Scan(&rows).Error
	if err != nil {
		return nil, 0, err
	}
	records := make([]ScoreRecord, 0, len(rows))
	for _, row := range rows {
		records = append(records, row.ScoreRecord)
	}
	if len(rows) == 0 {
		return records, 0, nil
	}
	return records, rows[0].Total, nil
}

// DailyWinners returns the winning valid attempt for each local game date in a
// period, oldest first (at most one row per day). Empty guildID spans all
// guilds. Within a day the winner is the lowest non-negative score, matching the
// leaderboard's ranking.
func (s *Store) DailyWinners(guildID string, period Period, now time.Time) ([]ScoreRecord, error) {
	start, end, err := historyBounds(period, now)
	if err != nil {
		return nil, err
	}
	return s.DailyWinnersBetween(guildID, start, end)
}

// DailyWinnersBetween is DailyWinners for an explicit half-open [start, end)
// window. Rows are read in score order, so the first row seen for a local day is
// that day's winner; day grouping uses the bot's local timezone to match the
// calendar boundaries used elsewhere. When a window holds more than maxDailyRows
// days, the most recent days are returned.
func (s *Store) DailyWinnersBetween(guildID string, start, end time.Time) ([]ScoreRecord, error) {
	from, args := historyQuery(start, end, guildID, "")
	query := `SELECT ` + recordColumns + `
		` + from + `
		ORDER BY s.score, julianday(g.game_date), s.message_id, p.user_id`
	s.mu.Lock()
	defer s.mu.Unlock()
	var rows []ScoreRecord
	if err := s.db.Raw(query, args...).Scan(&rows).Error; err != nil {
		return nil, err
	}

	best := make(map[string]ScoreRecord, len(rows))
	order := make([]string, 0, len(rows))
	for _, row := range rows {
		key := row.GameDate.In(time.Local).Format("2006-01-02")
		if _, ok := best[key]; ok {
			continue
		}
		best[key] = row
		order = append(order, key)
	}
	sort.Strings(order)
	if len(order) > maxDailyRows {
		order = order[len(order)-maxDailyRows:]
	}
	winners := make([]ScoreRecord, 0, len(order))
	for _, key := range order {
		winners = append(winners, best[key])
	}
	return winners, nil
}
