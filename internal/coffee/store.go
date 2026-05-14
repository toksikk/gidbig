package coffee

import (
	"errors"
	"log/slog"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// UserBeveragePreference persists a Discord user's preferred beverage emoji.
type UserBeveragePreference struct {
	gorm.Model
	UserID        string `gorm:"not null;uniqueIndex"`
	BeverageEmoji string `gorm:"not null"`
	HasSeenIntro  bool   `gorm:"not null;default:false"`
}

func (UserBeveragePreference) TableName() string { return "coffee_user_beverage_preferences" }

// UserGreeting records when a Discord user received their daily greeting reaction.
type UserGreeting struct {
	gorm.Model
	UserID    string    `gorm:"not null;index"`
	GreetedAt time.Time `gorm:"not null;index"`
}

func (UserGreeting) TableName() string { return "coffee_user_greetings" }

func (m *Module) getDB() *gorm.DB {
	m.dbMu.RLock()
	defer m.dbMu.RUnlock()
	return m.db
}

func (m *Module) openStore(path string) error {
	m.dbMu.Lock()
	defer m.dbMu.Unlock()
	var err error
	m.db, err = gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		return err
	}
	return m.db.AutoMigrate(&UserBeveragePreference{}, &UserGreeting{})
}

func (m *Module) closeStore() error {
	m.dbMu.Lock()
	defer m.dbMu.Unlock()
	if m.db == nil {
		return nil
	}
	sqlDB, err := m.db.DB()
	if err != nil {
		slog.Error("coffee: error getting sql.DB for close", "error", err)
		return err
	}
	if err := sqlDB.Close(); err != nil {
		slog.Error("coffee: error closing database", "error", err)
		return err
	}
	m.db = nil
	return nil
}

func (m *Module) getBeverageEmoji(userID string) (string, bool) {
	d := m.getDB()
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

func (m *Module) isUserIntroduced(userID string) bool {
	d := m.getDB()
	if d == nil {
		return false
	}
	var pref UserBeveragePreference
	result := d.Where("user_id = ?", userID).First(&pref)
	if result.Error != nil {
		return false
	}
	return pref.HasSeenIntro
}

func (m *Module) markUserIntroduced(userID string) error {
	d := m.getDB()
	if d == nil {
		return errors.New("store not initialized")
	}
	var pref UserBeveragePreference
	return d.Where(UserBeveragePreference{UserID: userID}).
		Attrs(UserBeveragePreference{BeverageEmoji: fallbackBeverage}).
		Assign(UserBeveragePreference{HasSeenIntro: true}).
		FirstOrCreate(&pref).Error
}

func (m *Module) setBeverageEmoji(userID, emoji string) error {
	d := m.getDB()
	if d == nil {
		return errors.New("store not initialized")
	}
	var pref UserBeveragePreference
	result := d.Where(UserBeveragePreference{UserID: userID}).
		Assign(UserBeveragePreference{BeverageEmoji: emoji, HasSeenIntro: true}).
		FirstOrCreate(&pref)
	return result.Error
}

func (m *Module) hasGreetedToday(userID string) bool {
	d := m.getDB()
	if d == nil {
		return false
	}

	now := m.nowFunc().UTC()
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

func (m *Module) recordGreeting(userID string) error {
	d := m.getDB()
	if d == nil {
		return errors.New("store not initialized")
	}

	return d.Transaction(func(tx *gorm.DB) error {
		now := m.nowFunc().UTC()
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
			return nil
		}

		return tx.Create(&UserGreeting{
			UserID:    userID,
			GreetedAt: now,
		}).Error
	})
}
