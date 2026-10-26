package trivia

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newTestStore(t *testing.T) *store {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "trivia.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	st, err := newStore(db)
	if err != nil {
		t.Fatalf("newStore: %v", err)
	}
	return st
}

func TestStore_IncrementAndFloor(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	for _, d := range []int64{2, 3, -1} {
		if err := st.add(ctx, "g", "u", at, statWords, d); err != nil {
			t.Fatalf("add: %v", err)
		}
	}
	var row DailyCount
	st.db.Where("user_id = ?", "u").Take(&row)
	if row.Count != 4 {
		t.Fatalf("count = %d, want 4", row.Count)
	}
	if err := st.add(ctx, "g", "u", at, statWords, -10); err != nil {
		t.Fatalf("add: %v", err)
	}
	st.db.Where("user_id = ?", "u").Take(&row)
	if row.Count != 0 {
		t.Fatalf("count = %d, want floored 0", row.Count)
	}
	// A decrement with no row creates nothing.
	if err := st.add(ctx, "g", "nobody", at, statWords, -1); err != nil {
		t.Fatalf("add: %v", err)
	}
	var n int64
	st.db.Model(&DailyCount{}).Where("user_id = ?", "nobody").Count(&n)
	if n != 0 {
		t.Fatalf("rows for nobody = %d, want 0", n)
	}
}

func TestStore_LeaderWindowsConsentAndMinimum(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	old := now.AddDate(0, 0, -40)
	mustAdd := func(user string, at time.Time, n int64) {
		t.Helper()
		if err := st.add(ctx, "g", user, at, statMessages, n); err != nil {
			t.Fatalf("add: %v", err)
		}
	}
	mustAdd("a", old, 100) // all-time leader, absent from the window
	mustAdd("b", now, 10)
	mustAdd("c", now, 10)
	mustAdd("d", now, 5)
	mustAdd("x", now, 1000) // privacy on at read time
	// Other guild must not leak in.
	if err := st.add(ctx, "other", "z", now, statMessages, 5000); err != nil {
		t.Fatal(err)
	}
	counted := func(u string) bool { return u != "x" }

	l, ok, err := st.leaderFor(ctx, "g", statMessages, "", counted)
	if err != nil || !ok || l.UserID != "a" || l.Total != 100 {
		t.Fatalf("all-time = %+v ok=%v err=%v, want a/100", l, ok, err)
	}
	since := dayOf(now.AddDate(0, 0, -(windowDays - 1)))
	l, ok, err = st.leaderFor(ctx, "g", statMessages, since, counted)
	if err != nil || !ok || l.UserID != "b" || l.Total != 10 {
		t.Fatalf("30d = %+v ok=%v err=%v, want b/10 (stable tie-break)", l, ok, err)
	}
	// Hiding d leaves only b and c in the window: below the minimum.
	_, ok, err = st.leaderFor(ctx, "g", statMessages, since, func(u string) bool { return u == "b" || u == "c" })
	if err != nil || ok {
		t.Fatalf("expected no leader with 2 participants, ok=%v err=%v", ok, err)
	}
}

func TestStore_AuthorIndexAndPrune(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	now := time.Now()
	if err := st.indexAuthor(ctx, "m1", "g", "u1", now.AddDate(0, 0, -31)); err != nil {
		t.Fatal(err)
	}
	if err := st.indexAuthor(ctx, "m2", "g", "u2", now); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.authorOf(ctx, "m1"); got != "u1" {
		t.Fatalf("authorOf(m1) = %q", got)
	}
	n, err := st.pruneAuthors(ctx, now.AddDate(0, 0, -windowDays))
	if err != nil || n != 1 {
		t.Fatalf("prune = %d, %v; want 1", n, err)
	}
	if got, _ := st.authorOf(ctx, "m1"); got != "" {
		t.Fatalf("m1 survived prune: %q", got)
	}
	if got, _ := st.authorOf(ctx, "m2"); got != "u2" {
		t.Fatalf("authorOf(m2) = %q", got)
	}
}

func TestStore_GuildState(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	if got, err := st.lastDropin(ctx, "g"); err != nil || !got.IsZero() {
		t.Fatalf("initial = %v, %v", got, err)
	}
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	for _, v := range []time.Time{at.Add(-time.Hour), at} {
		if err := st.setLastDropin(ctx, "g", v); err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := st.lastDropin(ctx, "g"); !got.Equal(at) {
		t.Fatalf("lastDropin = %v, want %v", got, at)
	}
}
