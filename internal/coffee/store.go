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

// TableName returns the database table name.
func (UserBeveragePreference) TableName() string { return "coffee_user_beverage_preferences" }

// UserGreeting records when a Discord user received their daily greeting reaction.
type UserGreeting struct {
	gorm.Model
	UserID    string    `gorm:"not null;index"`
	GreetedAt time.Time `gorm:"not null;index"`
}

// TableName returns the database table name.
func (UserGreeting) TableName() string { return "coffee_user_greetings" }

// MachineInventory holds the current consumable levels of a single guild's
// coffee machine. Levels are metric: beans and grounds in grams, water and
// milk in milliliters. Exactly one row exists per guild.
type MachineInventory struct {
	gorm.Model
	GuildID            string `gorm:"not null;uniqueIndex"`
	BeansMildGrams     int    `gorm:"not null"`
	BeansEspressoGrams int    `gorm:"not null"`
	WaterMl            int    `gorm:"not null"`
	MilkMl             int    `gorm:"not null"`
	GroundsGrams       int    `gorm:"not null"`
}

// TableName returns the database table name.
func (MachineInventory) TableName() string { return "coffee_machine_inventory" }

// RefillEvent records a single refill or grounds-empty action, attributing the
// amount (always a positive magnitude, in g or ml) to the user who performed it.
// Part "grounds" denotes a grounds-container empty; all other parts are refills.
type RefillEvent struct {
	gorm.Model
	GuildID string `gorm:"not null;index"`
	UserID  string `gorm:"not null;index"`
	Part    string `gorm:"not null"`
	Amount  int    `gorm:"not null"`
}

// TableName returns the database table name.
func (RefillEvent) TableName() string { return "coffee_refill_events" }

// DrinkEvent records a single drink dispensed, for consumption stats.
type DrinkEvent struct {
	gorm.Model
	GuildID   string `gorm:"not null;index"`
	UserID    string `gorm:"not null;index"`
	Drink     string `gorm:"not null"`
	WithMilk  bool   `gorm:"not null"`
	WithSugar bool   `gorm:"not null"`
}

// TableName returns the database table name.
func (DrinkEvent) TableName() string { return "coffee_drink_events" }

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
	return m.db.AutoMigrate(&UserBeveragePreference{}, &UserGreeting{},
		&MachineInventory{}, &RefillEvent{}, &DrinkEvent{})
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

// userCount is a per-user aggregate row used by the machine stats leaderboards.
type userCount struct {
	UserID string
	Count  int
}

// topDrinkers returns the users who dispensed the most drinks in the guild,
// most first, capped at limit.
func (m *Module) topDrinkers(guildID string, limit int) ([]userCount, error) {
	d := m.getDB()
	if d == nil {
		return nil, errors.New("store not initialized")
	}
	var rows []userCount
	err := d.Model(&DrinkEvent{}).
		Select("user_id, count(*) as count").
		Where("guild_id = ?", guildID).
		Group("user_id").
		Order("count DESC, user_id ASC").
		Limit(limit).
		Scan(&rows).Error
	return rows, err
}

// topRefillers returns the users with the most refill actions (grounds-empties
// excluded) in the guild, most first, capped at limit.
func (m *Module) topRefillers(guildID string, limit int) ([]userCount, error) {
	d := m.getDB()
	if d == nil {
		return nil, errors.New("store not initialized")
	}
	var rows []userCount
	err := d.Model(&RefillEvent{}).
		Select("user_id, count(*) as count").
		Where("guild_id = ? AND part <> ?", guildID, partGrounds).
		Group("user_id").
		Order("count DESC, user_id ASC").
		Limit(limit).
		Scan(&rows).Error
	return rows, err
}

// groundsEmptiedCount returns how many times the grounds container was emptied
// in the guild.
func (m *Module) groundsEmptiedCount(guildID string) (int64, error) {
	d := m.getDB()
	if d == nil {
		return 0, errors.New("store not initialized")
	}
	var c int64
	err := d.Model(&RefillEvent{}).
		Where("guild_id = ? AND part = ?", guildID, partGrounds).
		Count(&c).Error
	return c, err
}
