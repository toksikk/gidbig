package datastore

import (
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

type historyFixture struct {
	t     *testing.T
	store *Store
	now   time.Time
}

func newHistoryFixture(t *testing.T) *historyFixture {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return &historyFixture{t: t, store: store, now: time.Date(2026, time.March, 10, 14, 0, 0, 0, time.Local)}
}

func (f *historyFixture) add(userID, guildID, channelID, messageID string, date time.Time, score int) {
	f.t.Helper()
	player, err := f.store.EnsurePlayer(userID)
	if err != nil {
		f.t.Fatal(err)
	}
	season, err := f.store.EnsureSeason(date)
	if err != nil {
		f.t.Fatal(err)
	}
	game, err := f.store.EnsureGame(channelID, guildID, date, season.ID)
	if err != nil {
		f.t.Fatal(err)
	}
	if err := f.store.CreateScore(messageID, player.ID, score, game.ID); err != nil {
		f.t.Fatal(err)
	}
}

func recordMessages(records []ScoreRecord) []string {
	ids := make([]string, 0, len(records))
	for _, record := range records {
		ids = append(ids, record.MessageID)
	}
	return ids
}

func expectMessages(t *testing.T, records []ScoreRecord, want ...string) {
	t.Helper()
	if got := recordMessages(records); !reflect.DeepEqual(got, want) {
		t.Errorf("message IDs = %v, want %v", got, want)
	}
}

func TestTopPlayers(t *testing.T) {
	f := newHistoryFixture(t)
	day := f.now.Add(-24 * time.Hour)
	f.add("alice", "one", "c1", "a-worse", day, 20)
	f.add("alice", "one", "c2", "a-best", day, 1)
	f.add("alice", "other", "c3", "a-other", day, 0) // Must not replace guild best.
	f.add("bob", "one", "c2", "b-later", day.Add(time.Hour), 1)
	f.add("bob", "one", "c1", "b-first", day, 1)
	f.add("carol", "one", "c1", "c-zero", day, 0)
	f.add("early", "one", "c1", "negative", day, -1)
	f.add("old", "one", "c1", "previous-month", f.now.AddDate(0, -1, 0), 0)
	f.add("future", "one", "c1", "future", f.now, 0)

	got, err := f.store.TopPlayers("one", "", f.now)
	if err != nil {
		t.Fatal(err)
	}
	expectMessages(t, got, "c-zero", "a-best", "b-first")
	if len(got) != 3 || got[1].Score != 1 || got[1].UserID != "alice" || got[1].GuildID != "one" || got[1].ChannelID != "c2" || !got[1].GameDate.Equal(day) {
		t.Errorf("unexpected record details: %+v", got)
	}

	// Equal scores and dates use message ID, then user ID, for stable ordering.
	f.add("dan", "one", "c1", "a-tie", day, 1)
	got, err = f.store.TopPlayers("one", PeriodMonth, f.now)
	if err != nil {
		t.Fatal(err)
	}
	expectMessages(t, got, "c-zero", "a-best", "a-tie", "b-first")

	for i := 0; i < 12; i++ {
		f.add(fmt.Sprintf("player-%02d", i), "one", "c1", fmt.Sprintf("m-%02d", i), day, 2+i)
	}
	got, err = f.store.TopPlayers("one", PeriodMonth, f.now)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 10 {
		t.Errorf("leaderboard size = %d, want 10", len(got))
	}
	expectMessages(t, got, "c-zero", "a-best", "a-tie", "b-first", "m-00", "m-01", "m-02", "m-03", "m-04", "m-05")

	empty, err := f.store.TopPlayers("missing", PeriodAllTime, f.now)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Errorf("empty leaderboard = %v, %v", empty, err)
	}
	global, err := f.store.TopPlayers("", PeriodMonth, f.now)
	if err != nil {
		t.Fatal(err)
	}
	if len(global) != 10 || global[0].GuildID != "other" || global[0].UserID != "alice" {
		t.Errorf("global leaderboard = %+v", global)
	}
}

func TestPlayerRecords(t *testing.T) {
	f := newHistoryFixture(t)
	day := f.now.Add(-24 * time.Hour)
	f.add("alice", "one", "c1", "worst", day, 50)
	f.add("alice", "one", "c2", "tie-b", day, 0)
	f.add("alice", "one", "c1", "tie-a", day, 0)
	f.add("alice", "other", "c3", "global", day, 1)
	f.add("alice", "one", "c1", "early", day, -1)
	f.add("alice", "one", "c1", "deleted", day, 0)
	if err := f.store.db.Where("message_id = ?", "deleted").Delete(&Score{}).Error; err != nil {
		t.Fatal(err)
	}
	f.add("bob", "one", "c1", "bob", day, 0)
	for i := 0; i < 11; i++ {
		f.add("alice", "one", "c1", fmt.Sprintf("extra-%02d", i), day, 100+i)
	}

	got, count, err := f.store.PlayerRecords("alice", "one", PeriodMonth, f.now)
	if err != nil {
		t.Fatal(err)
	}
	if count != 14 || len(got) != 10 {
		t.Errorf("guild results count=%d records=%d, want 14 and 10", count, len(got))
	}
	expectMessages(t, got, "tie-a", "tie-b", "worst", "extra-00", "extra-01", "extra-02", "extra-03", "extra-04", "extra-05", "extra-06")
	if got[0].UserID != "alice" || got[0].ChannelID != "c1" || got[0].Score != 0 || !got[0].GameDate.Equal(day) {
		t.Errorf("unexpected record details: %+v", got[0])
	}

	global, count, err := f.store.PlayerRecords("alice", "", PeriodMonth, f.now)
	if err != nil {
		t.Fatal(err)
	}
	if count != 15 {
		t.Errorf("global count = %d, want 15", count)
	}
	expectMessages(t, global, "tie-a", "tie-b", "global", "worst", "extra-00", "extra-01", "extra-02", "extra-03", "extra-04", "extra-05")
	if global[2].GuildID != "other" {
		t.Errorf("global record = %+v", global[2])
	}
	for _, record := range global {
		if record.UserID != "alice" {
			t.Errorf("other user's record: %+v", record)
		}
	}

	empty, count, err := f.store.PlayerRecords("absent", "", PeriodAllTime, f.now)
	if err != nil || empty == nil || len(empty) != 0 || count != 0 {
		t.Errorf("empty records = %v, %d, %v", empty, count, err)
	}
}

func TestHistoryReadsShiftedLegacyGames(t *testing.T) {
	f := newHistoryFixture(t)
	day := f.now.Add(-24 * time.Hour)
	const guild = "225303764108705793"
	const otherGuild = "125231125961506816"
	f.add("alice", guild, "recent-channel", "recent", day, 9)
	f.add("alice", otherGuild, "other-channel", "other", day, 2)
	player, err := f.store.EnsurePlayer("alice")
	if err != nil {
		t.Fatal(err)
	}
	season, err := f.store.EnsureSeason(day)
	if err != nil {
		t.Fatal(err)
	}
	// Production rows before August 2026 have game_date=season ID,
	// guild_id=game date, and season_id=guild snowflake.
	result := f.store.db.Exec(`INSERT INTO leetoclock_games (channel_id, guild_id, game_date, season_id) VALUES (?, ?, ?, ?)`, "old-channel", day.Format("2006-01-02 15:04:05-07:00"), season.ID, guild)
	if result.Error != nil {
		t.Fatal(result.Error)
	}
	var legacy Game
	if err := f.store.db.Where("channel_id = ?", "old-channel").First(&legacy).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.store.CreateScore("legacy", player.ID, 0, legacy.ID); err != nil {
		t.Fatal(err)
	}

	records, err := f.store.TopPlayers(guild, PeriodMonth, f.now)
	if err != nil {
		t.Fatal(err)
	}
	expectMessages(t, records, "legacy")
	if len(records) != 1 || records[0].GuildID != guild || !records[0].GameDate.Equal(day) || records[0].Score != 0 || records[0].ChannelID != "old-channel" {
		t.Errorf("legacy top details: %+v", records)
	}
	global, err := f.store.TopPlayers("", PeriodMonth, f.now)
	if err != nil {
		t.Fatal(err)
	}
	expectMessages(t, global, "legacy")
	playerRecords, count, err := f.store.PlayerRecords("alice", guild, PeriodMonth, f.now)
	if err != nil {
		t.Fatal(err)
	}
	expectMessages(t, playerRecords, "legacy", "recent")
	if count != 2 {
		t.Errorf("server count = %d, want 2", count)
	}
	playerRecords, count, err = f.store.PlayerRecords("alice", "", PeriodMonth, f.now)
	if err != nil {
		t.Fatal(err)
	}
	expectMessages(t, playerRecords, "legacy", "other", "recent")
	if count != 3 {
		t.Errorf("global count = %d, want 3", count)
	}
	previous, err := f.store.TopPlayers(guild, PeriodLast7Days, day.Add(-time.Hour))
	if err != nil || len(previous) != 0 {
		t.Errorf("before legacy date: %+v, %v", previous, err)
	}
}

func TestHistoryPeriods(t *testing.T) {
	f := newHistoryFixture(t)
	monthStart := time.Date(2026, time.March, 1, 0, 0, 0, 0, time.Local)
	cases := []struct {
		id   string
		date time.Time
	}{
		{"month-before", monthStart.Add(-time.Second)},
		{"month-start", monthStart},
		{"seven-before", f.now.Add(-7*24*time.Hour - time.Second)},
		{"seven-start", f.now.Add(-7 * 24 * time.Hour)},
		{"thirty-before", f.now.Add(-30*24*time.Hour - time.Second)},
		{"thirty-start", f.now.Add(-30 * 24 * time.Hour)},
		{"end-before", f.now.Add(-time.Second)},
		{"end", f.now},
		{"future", f.now.Add(time.Second)},
	}
	for _, tc := range cases {
		f.add("alice", "one", "c1", tc.id, tc.date, 0)
	}
	for _, tc := range []struct {
		period Period
		want   []string
	}{
		{"", []string{"month-start", "seven-before", "seven-start", "end-before"}},
		{PeriodLast7Days, []string{"seven-start", "end-before"}},
		{PeriodLast30Days, []string{"thirty-start", "month-before", "month-start", "seven-before", "seven-start", "end-before"}},
		{PeriodAllTime, []string{"thirty-before", "thirty-start", "month-before", "month-start", "seven-before", "seven-start", "end-before"}},
	} {
		t.Run(string(tc.period), func(t *testing.T) {
			got, count, err := f.store.PlayerRecords("alice", "one", tc.period, f.now)
			if err != nil {
				t.Fatal(err)
			}
			if count != int64(len(tc.want)) {
				t.Errorf("count = %d, want %d", count, len(tc.want))
			}
			expectMessages(t, got, tc.want...)
		})
	}
}

func TestHistoryLocalMonthAndRollingDST(t *testing.T) {
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	previousLocal := time.Local
	time.Local = location
	t.Cleanup(func() { time.Local = previousLocal })
	f := newHistoryFixture(t)
	// March 1 uses UTC-5; March 10 uses UTC-4. Calendar month must
	// start at local midnight, while rolling windows use elapsed hours.
	monthStart := time.Date(2026, time.March, 1, 0, 0, 0, 0, location)
	f.add("alice", "one", "c1", "before-month", monthStart.Add(-time.Second), 1)
	f.add("alice", "one", "c1", "month-start", monthStart, 1)
	f.add("alice", "one", "c1", "seven-before", f.now.Add(-7*24*time.Hour-time.Second), 1)
	f.add("alice", "one", "c1", "seven-start", f.now.Add(-7*24*time.Hour), 1)
	for _, tc := range []struct {
		period Period
		want   []string
	}{
		{PeriodMonth, []string{"month-start", "seven-before", "seven-start"}},
		{PeriodLast7Days, []string{"seven-start"}},
	} {
		got, count, err := f.store.PlayerRecords("alice", "", tc.period, f.now)
		if err != nil {
			t.Fatal(err)
		}
		if count != int64(len(tc.want)) {
			t.Errorf("%s count = %d, want %d", tc.period, count, len(tc.want))
		}
		expectMessages(t, got, tc.want...)
	}
}

func TestHistoryReadErrors(t *testing.T) {
	f := newHistoryFixture(t)
	if _, err := f.store.TopPlayers("one", "invalid", f.now); err == nil {
		t.Error("invalid period accepted")
	}
	if _, _, err := f.store.PlayerRecords("alice", "", "invalid", f.now); err == nil {
		t.Error("invalid period accepted")
	}
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.TopPlayers("one", PeriodMonth, f.now); err == nil {
		t.Error("failed leaderboard read returned no error")
	}
	if _, _, err := f.store.PlayerRecords("alice", "", PeriodMonth, f.now); err == nil {
		t.Error("failed player read returned no error")
	}
}
