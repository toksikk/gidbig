package coffee

import (
	"errors"
	"log/slog"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// UserBeveragePreference persists a Discord user's preferred beverage emoji.
type UserBeveragePreference struct {
	gorm.Model
	UserID        string `gorm:"not null;uniqueIndex"`
	BeverageEmoji string `gorm:"not null"`
}

var db *gorm.DB

func openStore(path string) error {
	var err error
	db, err = gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		return err
	}
	return db.AutoMigrate(&UserBeveragePreference{})
}

func closeStore() {
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
}

func getBeverageEmoji(userID string) (string, bool) {
	if db == nil {
		return "", false
	}
	var pref UserBeveragePreference
	result := db.Where("user_id = ?", userID).First(&pref)
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
	if db == nil {
		return errors.New("store not initialized")
	}
	var pref UserBeveragePreference
	result := db.Where(UserBeveragePreference{UserID: userID}).Assign(UserBeveragePreference{BeverageEmoji: emoji}).FirstOrCreate(&pref)
	return result.Error
}
