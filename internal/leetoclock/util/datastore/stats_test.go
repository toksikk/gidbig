package datastore

import (
	"context"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestStoreCounts(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file::memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&Player{}, &Season{}, &Game{}, &Score{}, &Highscore{}); err != nil {
		t.Fatal(err)
	}
	s := NewStore(db)

	now := time.Date(2026, 10, 9, 13, 37, 0, 0, time.UTC)
	last := now.AddDate(0, -1, 0)
	for _, d := range []time.Time{last, now} {
		season, err := s.EnsureSeason(d)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.EnsureGame("c", "g", d, season.ID); err != nil {
			t.Fatal(err)
		}
	}
	p1, _ := s.EnsurePlayer("u1")
	p2, _ := s.EnsurePlayer("u2")
	mustScore := func(msg string, pid, gid uint) {
		if err := s.CreateScore(msg, pid, 1, gid); err != nil {
			t.Fatal(err)
		}
	}
	mustScore("m1", p1.ID, 1)
	mustScore("m2", p2.ID, 1)
	mustScore("m3", p1.ID, 2)

	c, err := s.Counts(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	want := Counts{Players: 2, Games: 2, Scores: 3, Seasons: 2, SeasonGames: 1, SeasonPlayers: 1}
	if c != want {
		t.Fatalf("Counts = %+v, want %+v", c, want)
	}
}
