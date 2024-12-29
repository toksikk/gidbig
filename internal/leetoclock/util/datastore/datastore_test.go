package datastore

import (
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestStore_EnsureSeason(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file::memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	store := &Store{db: db}

	err = store.db.AutoMigrate(&Season{})
	if err != nil {
		t.Fatalf("failed to migrate database: %v", err)
	}

	tests := []struct {
		name          string
		date          time.Time
		wantErr       bool
		wantStartDate time.Time
		wantEndDate   time.Time
	}{
		{
			name:          "season 1",
			date:          time.Date(2023, time.July, 8, 0, 0, 0, 0, time.UTC),
			wantErr:       false,
			wantStartDate: time.Date(2023, time.July, 1, 0, 0, 0, 0, time.UTC),
			wantEndDate:   time.Date(2023, time.July, 31, 23, 59, 59, 999999999, time.UTC),
		},
		{
			name:          "season 2",
			date:          time.Date(2022, time.January, 1, 0, 0, 0, 0, time.UTC),
			wantErr:       false,
			wantStartDate: time.Date(2022, time.January, 1, 0, 0, 0, 0, time.UTC),
			wantEndDate:   time.Date(2022, time.January, 31, 23, 59, 59, 999999999, time.UTC),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			season, err := store.EnsureSeason(tt.date)
			if (err != nil) != tt.wantErr {
				t.Errorf("Store.EnsureSeason() error = %v, wantErr %v", err, tt.wantErr)
			}
			if season.StartDate != tt.wantStartDate {
				t.Errorf("Store.EnsureSeason() season.StartDate = %v, wantStartDate %v", season.StartDate, tt.wantStartDate)
			}
			if season.EndDate != tt.wantEndDate {
				t.Errorf("Store.EnsureSeason() season.EndDate = %v, wantEndDate %v", season.EndDate, tt.wantEndDate)
			}
		})
	}
}

func TestStore_CreateScore(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file::memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	store := &Store{db: db}

	err = store.db.AutoMigrate(&Score{})
	if err != nil {
		t.Fatalf("failed to migrate database: %v", err)
	}

	tests := []struct {
		name      string
		messageID string
		playerID  uint
		score     int
		gameID    uint
	}{
		{
			name:      "score 1",
			messageID: "123",
			playerID:  1,
			score:     100,
			gameID:    1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := store.CreateScore(tt.messageID, tt.playerID, tt.score, tt.gameID)
			if err != nil {
				t.Fatalf("Store.CreateScore() error = %v", err)
			}
			scores, err := store.GetScores()
			if err != nil {
				t.Fatalf("Store.GetScores() error = %v", err)
			}
			if len(scores) != 1 {
				t.Fatalf("Store.GetScores() len(scores) = %v, want 1", len(scores))
			}
			if scores[0].MessageID != tt.messageID {
				t.Errorf("Store.GetScores() scores[0].MessageID = %v, want %v", scores[0].MessageID, tt.messageID)
			}
			if scores[0].PlayerID != tt.playerID {
				t.Errorf("Store.GetScores() scores[0].PlayerID = %v, want %v", scores[0].PlayerID, tt.playerID)
			}
			if scores[0].Score != tt.score {
				t.Errorf("Store.GetScores() scores[0].Score = %v, want %v", scores[0].Score, tt.score)
			}
			if scores[0].GameID != tt.gameID {
				t.Errorf("Store.GetScores() scores[0].GameID = %v, want %v", scores[0].GameID, tt.gameID)
			}
		})
	}
}

func TestStore_GetSeasons(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file::memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	store := &Store{db: db}

	err = store.db.AutoMigrate(&Season{})
	if err != nil {
		t.Fatalf("failed to migrate database: %v", err)
	}

	tests := []struct {
		name    string
		create  []time.Time
		want    []Season
		wantErr bool
	}{
		{
			name: "one season",
			create: []time.Time{
				time.Date(2021, time.January, 1, 0, 0, 0, 0, time.UTC),
			},
			want: []Season{
				{
					StartDate: time.Date(2021, time.January, 1, 0, 0, 0, 0, time.UTC),
					EndDate:   time.Date(2021, time.January, 31, 23, 59, 59, 999999999, time.UTC),
				},
			},
			wantErr: false,
		},
		{
			name: "two seasons",
			create: []time.Time{
				time.Date(2021, time.January, 1, 0, 0, 0, 0, time.UTC),
				time.Date(2021, time.July, 15, 13, 37, 5, 69696969, time.UTC),
			},
			want: []Season{
				{
					StartDate: time.Date(2021, time.January, 1, 0, 0, 0, 0, time.UTC),
					EndDate:   time.Date(2021, time.January, 31, 23, 59, 59, 999999999, time.UTC),
				},
				{
					StartDate: time.Date(2021, time.July, 1, 0, 0, 0, 0, time.UTC),
					EndDate:   time.Date(2021, time.July, 31, 23, 59, 59, 999999999, time.UTC),
				},
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := store.db.Migrator().DropTable(&Season{})
			if err != nil {
				t.Fatalf("failed to drop table: %v", err)
			}
			err = store.db.AutoMigrate(&Season{})
			if err != nil {
				t.Fatalf("failed to migrate database: %v", err)
			}

			for _, time := range tt.create {
				_, err := store.EnsureSeason(time)
				if err != nil {
					t.Fatalf("failed to create season: %v", err)
				}
			}

			got, err := store.GetSeasons()
			if (err != nil) != tt.wantErr {
				t.Errorf("Store.GetSeasons() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if len(got) != len(tt.want) {
				t.Errorf("Store.GetSeasons() len(got) = %v, len(want) %v", len(got), len(tt.want))
			}
		})
	}
}
