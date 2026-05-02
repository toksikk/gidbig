package coffee

import (
	"errors"
	"log/slog"
	"sync"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// UserBeveragePreference persists a Discord user's preferred beverage emoji.
type UserBeveragePreference struct {
	gorm.Model
	UserID        string `gorm:"not null;uniqueIndex"`
	BeverageEmoji string `gorm:"not null"`
}

var (
	dbMu sync.RWMutex
	db   *gorm.DB
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
	return db.AutoMigrate(&UserBeveragePreference{})
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
