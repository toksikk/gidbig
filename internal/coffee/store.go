package coffee

import (
	"errors"
	"log/slog"
	"sync"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// UserBeveragePreference persists a Discord user's preferred beverage emoji.
type UserBeveragePreference struct {
	gorm.Model
	UserID        string `gorm:"not null;uniqueIndex"`
	BeverageEmoji string `gorm:"not null"`
}

// UserGreeting records when a Discord user received their daily greeting reaction.
type UserGreeting struct {
	gorm.Model
	UserID    string    `gorm:"not null;index"`
	GreetedAt time.Time `gorm:"not null;index"`
}

var (
	dbMu    sync.RWMutex
	db      *gorm.DB
	nowFunc = time.Now
)

func getDB() *gorm.DB {
	dbMu.RLock()
	defer dbMu.RUnlock()
	return db
}

func openStore(path string) error {
	dbMu.Lock()
	defer dbMu.Unlock()
	var err error
	db, err = gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		return err
	}
	return db.AutoMigrate(&UserBeveragePreference{}, &UserGreeting{})
}

func closeStore() {
	dbMu.Lock()
	defer dbMu.Unlock()
	if db == nil {
		return
	}
	sqlDB, err := db.DB()
	if err != nil {
		slog.Error("coffee: error getting sql.DB for close", "error", err)
		return
	}
	if err := sqlDB.Close(); err != nil {
		slog.Error("coffee: error closing database", "error", err)
	}
	db = nil
}

func getBeverageEmoji(userID string) (string, bool) {
	d := getDB()
	if d == nil {
		return "", false
	}
	var pref UserBeveragePreference
	result := d.Where("user_id = ?", userID).First(&pref)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return "", false
		}
		slog.Error("coffee: error querying beverage preference", "error", result.Error)
		return "", false
	}
	return pref.BeverageEmoji, true
}

func setBeverageEmoji(userID, emoji string) error {
	d := getDB()
	if d == nil {
		return errors.New("store not initialized")
	}
	var pref UserBeveragePreference
	result := d.Where(UserBeveragePreference{UserID: userID}).Assign(UserBeveragePreference{BeverageEmoji: emoji}).FirstOrCreate(&pref)
	return result.Error
}

func hasGreetedToday(userID string) bool {
	d := getDB()
	if d == nil {
		return false
	}

	now := nowFunc().UTC()
	year, month, day := now.Date()
	startOfToday := time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
	startOfTomorrow := startOfToday.AddDate(0, 0, 1)

	var count int64
	result := d.Model(&UserGreeting{}).
		Where("user_id = ? AND greeted_at >= ? AND greeted_at < ?", userID, startOfToday, startOfTomorrow).
		Count(&count)
	if result.Error != nil {
		slog.Error("coffee: error querying daily greeting", "error", result.Error, "userID", userID)
		return false
	}
	return count > 0
}

func recordGreeting(userID string) error {
	d := getDB()
	if d == nil {
		return errors.New("store not initialized")
	}

	return d.Transaction(func(tx *gorm.DB) error {
		now := nowFunc().UTC()
		year, month, day := now.Date()
		startOfToday := time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
		startOfTomorrow := startOfToday.AddDate(0, 0, 1)

		var count int64
		if err := tx.Model(&UserGreeting{}).
			Where("user_id = ? AND greeted_at >= ? AND greeted_at < ?", userID, startOfToday, startOfTomorrow).
			Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return nil // already greeted, skip insert
		}

		return tx.Create(&UserGreeting{
			UserID:    userID,
			GreetedAt: now,
		}).Error
	})
}
