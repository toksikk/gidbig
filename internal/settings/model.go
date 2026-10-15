package settings

import "time"

// Setting is one persisted runtime override. The table is a plain key-value
// store, so a new tunable needs a registry entry, never a migration. Value is
// always the string form; the registry owns the type.
type Setting struct {
	Key       string `gorm:"primaryKey"`
	Value     string
	UpdatedBy string
	UpdatedAt time.Time
}

// TableName returns the shared-database table for runtime overrides.
func (Setting) TableName() string { return "settings" }
