package datastore

import (
	"fmt"
	"time"
)

// Period selects a game-date window for score-history queries.
type Period string

const (
	PeriodMonth      Period = "month"
	PeriodLast7Days  Period = "last7days"
	PeriodLast30Days Period = "last30days"
	PeriodAllTime    Period = "alltime"
	maxHistoryRows          = 10
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
	case PeriodLast7Days:
		return now.Add(-7 * 24 * time.Hour), now, nil
	case PeriodLast30Days:
		return now.Add(-30 * 24 * time.Hour), now, nil
	case PeriodAllTime:
		return time.Time{}, now, nil
	default:
		return time.Time{}, time.Time{}, fmt.Errorf("unknown score-history period %q", period)
	}
}

// Older databases contain games with guild_id, game_date, and season_id shifted
// one column to the right. Identify those rows by their numeric game_date and
// snowflake-sized season_id, then read their date and guild from the original
// values without changing the stored data. New games use the normal columns.
const legacyGame = `typeof(g.game_date) = 'integer' AND g.season_id > 10000000000000000 AND julianday(g.guild_id) IS NOT NULL`
const gameDate = `CASE WHEN ` + legacyGame + ` THEN g.guild_id ELSE g.game_date END`
const gameGuild = `CASE WHEN ` + legacyGame + ` THEN CAST(g.season_id AS TEXT) ELSE g.guild_id END`

// historyQuery builds the common joins and filters before any ranking or limit.
// julianday compares instants even when stored game dates have different UTC offsets.
func historyQuery(start, end time.Time, guildID, userID string) (string, []any) {
	query := `FROM leetoclock_scores AS s
		JOIN leetoclock_games AS g ON g.id = s.game_id
		JOIN leetoclock_players AS p ON p.id = s.player_id
		WHERE s.deleted_at IS NULL AND g.deleted_at IS NULL AND p.deleted_at IS NULL
			AND s.score >= 0 AND julianday(` + gameDate + `) < julianday(?)`
	args := []any{end}
	if !start.IsZero() {
		query += " AND julianday(" + gameDate + ") >= julianday(?)"
		args = append(args, start)
	}
	if guildID != "" {
		query += " AND " + gameGuild + " = ?"
		args = append(args, guildID)
	}
	if userID != "" {
		query += " AND p.user_id = ?"
		args = append(args, userID)
	}
	return query, args
}

const recordColumns = `s.score, p.user_id, ` + gameDate + ` AS game_date, ` + gameGuild + ` AS guild_id, g.channel_id, s.message_id`

// A CASE expression loses SQLite's DATETIME column type, so database/sql
// returns a string rather than time.Time. Decode both legacy and current dates.
type historyRow struct {
	Score     int
	UserID    string
	GameDate  string
	GuildID   string
	ChannelID string
	MessageID string
	Total     int64
}

func (row historyRow) record() (ScoreRecord, error) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999-07:00", "2006-01-02 15:04:05.999999999"} {
		if date, err := time.ParseInLocation(layout, row.GameDate, time.Local); err == nil {
			return ScoreRecord{Score: row.Score, UserID: row.UserID, GameDate: date,
				GuildID: row.GuildID, ChannelID: row.ChannelID, MessageID: row.MessageID}, nil
		}
	}
	return ScoreRecord{}, fmt.Errorf("invalid score-history game date")
}

// TopPlayers returns up to ten distinct players' best valid attempts.
// Empty guildID selects records across all guilds. Ties are resolved by score,
// game date, message ID, then user ID.
func (s *Store) TopPlayers(guildID string, period Period, now time.Time) ([]ScoreRecord, error) {
	start, end, err := historyBounds(period, now)
	if err != nil {
		return nil, err
	}
	from, args := historyQuery(start, end, guildID, "")
	query := `WITH ranked AS (
		SELECT ` + recordColumns + `,
			ROW_NUMBER() OVER (PARTITION BY s.player_id ORDER BY s.score, julianday(` + gameDate + `), s.message_id, p.user_id) AS rank
		` + from + `
	)
	SELECT score, user_id, game_date, guild_id, channel_id, message_id
	FROM ranked WHERE rank = 1
	ORDER BY score, julianday(game_date), message_id, user_id LIMIT ?`
	s.mu.Lock()
	defer s.mu.Unlock()
	var rows []historyRow
	err = s.db.Raw(query, append(args, maxHistoryRows)...).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	records := make([]ScoreRecord, 0, len(rows))
	for _, row := range rows {
		record, err := row.record()
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}

// PlayerRecords returns up to ten best valid attempts and the full valid attempt
// count for one user. Empty guildID selects that user's records across all guilds.
func (s *Store) PlayerRecords(userID, guildID string, period Period, now time.Time) ([]ScoreRecord, int64, error) {
	start, end, err := historyBounds(period, now)
	if err != nil {
		return nil, 0, err
	}
	if userID == "" {
		return nil, 0, fmt.Errorf("user ID required for player records")
	}
	from, args := historyQuery(start, end, guildID, userID)
	query := `SELECT ` + recordColumns + `, COUNT(*) OVER () AS total
		` + from + `
		ORDER BY s.score, julianday(` + gameDate + `), s.message_id LIMIT ?`
	var rows []historyRow
	s.mu.Lock()
	defer s.mu.Unlock()
	err = s.db.Raw(query, append(args, maxHistoryRows)...).Scan(&rows).Error
	if err != nil {
		return nil, 0, err
	}
	records := make([]ScoreRecord, 0, len(rows))
	for _, row := range rows {
		record, err := row.record()
		if err != nil {
			return nil, 0, err
		}
		records = append(records, record)
	}
	if len(rows) == 0 {
		return records, 0, nil
	}
	return records, rows[0].Total, nil
}
