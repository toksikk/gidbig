package settings

import (
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/toksikk/gidbig/internal/cfg"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrUnknownKey is returned by Set and Unset for a key that is not registered,
// or that is registered as secret. The two cases are deliberately
// indistinguishable so a caller cannot probe for a credential.
var ErrUnknownKey = errors.New("unknown or non-editable setting")

// Store holds the runtime overrides over the config loaded at boot. The
// database is the source of truth; the in-memory cache is derived and rebuilt
// from it, never mutated in place.
type Store struct {
	db *gorm.DB
	// base is the deploy-time config (config.yaml plus defaults) the overrides
	// layer over. It is never modified after New.
	base *cfg.Config
	// baseline carries only the built-in defaults, so the source of an
	// effective value can be told apart without duplicating the defaults.
	baseline *cfg.Config

	// mu serialises writes with the cache swap. Reads never take it: the cache
	// is an atomically published immutable snapshot, so hot read paths stay
	// lock-free.
	mu  sync.Mutex
	now atomic.Pointer[snapshot]
}

// snapshot is the immutable view of the stored overrides. It is replaced, never
// mutated, so a reader holding a pointer can safely keep using it.
type snapshot struct {
	values map[string]string
}

// New builds the store over the shared database. base is the config loaded at
// boot and supplies the values the overrides layer over; it must not be nil.
// The settings table is created when missing. The database handle is owned by
// the composition root and is never closed here.
func New(db *gorm.DB, base *cfg.Config) (*Store, error) {
	if db == nil {
		return nil, errors.New("settings: nil database")
	}
	if base == nil {
		return nil, errors.New("settings: nil base config")
	}
	// Decoding an empty document applies the built-in defaults while skipping
	// the required-field validation, which is exactly the baseline needed to
	// tell a default from an explicit config.yaml value.
	baseline, err := cfg.Decode(strings.NewReader("{}"))
	if err != nil {
		return nil, fmt.Errorf("settings: build defaults: %w", err)
	}
	if err := db.AutoMigrate(&Setting{}); err != nil {
		return nil, fmt.Errorf("settings: create table: %w", err)
	}
	s := &Store{db: db, base: base, baseline: baseline}
	if err := s.reload(); err != nil {
		return nil, err
	}
	return s, nil
}

// reload rebuilds the cache from the database. A crash between a write and the
// swap only costs staleness until the next reload or restart.
func (s *Store) reload() error {
	var rows []Setting
	if err := s.db.Find(&rows).Error; err != nil {
		return fmt.Errorf("settings: load overrides: %w", err)
	}
	values := make(map[string]string, len(rows))
	for _, row := range rows {
		k, ok := byName[row.Key]
		if !ok || k.Secret {
			slog.Warn("settings: ignoring a stored value for an unknown key", "key", row.Key)
			continue
		}
		values[row.Key] = row.Value
	}
	s.now.Store(&snapshot{values: values})
	return nil
}

// override returns the stored value for a key and whether a row exists.
func (s *Store) override(key string) (string, bool) {
	if s == nil {
		return "", false
	}
	snap := s.now.Load()
	if snap == nil {
		return "", false
	}
	v, ok := snap.values[key]
	return v, ok
}

// Override returns the stored override for key without the config.yaml or
// built-in fallback, and whether a row exists. Callers that resolve their own
// fallback (the llm client) use this rather than the typed getters. A secret
// key is never reported.
func (s *Store) Override(key string) (string, bool) {
	if k, ok := byName[key]; !ok || k.Secret {
		return "", false
	}
	return s.override(key)
}

// Effective returns the effective value for a key and where it came from. The
// second result is false for an unknown or secret key.
func (s *Store) Effective(key string) (string, Source, bool) {
	if s == nil {
		return "", "", false
	}
	k, ok := byName[key]
	if !ok || k.Secret {
		return "", "", false
	}
	if v, ok := s.override(key); ok {
		return v, SourceDB, true
	}
	v := k.Extract(s.base)
	if v == k.Extract(s.baseline) {
		return v, SourceDefault, true
	}
	return v, SourceYAML, true
}

// lookup returns the effective raw value for a known, non-secret key.
func (s *Store) lookup(key string) (Key, string, bool) {
	if s == nil {
		return Key{}, "", false
	}
	k, ok := byName[key]
	if !ok || k.Secret {
		return Key{}, "", false
	}
	if v, ok := s.override(key); ok {
		return k, v, true
	}
	return k, k.Extract(s.base), true
}

// Bool returns the effective value of a boolean key. A stored value that does
// not parse falls back to the built-in default.
func (s *Store) Bool(key string) bool {
	k, raw, ok := s.lookup(key)
	if !ok {
		return false
	}
	v, err := strconv.ParseBool(strings.TrimSpace(raw))
	if err != nil {
		slog.Warn("settings: stored value is not a boolean, using the default", "key", key, "value", raw)
		v, _ = strconv.ParseBool(k.Extract(s.baseline))
	}
	return v
}

// Int returns the effective value of an integer key. A stored value that does
// not parse falls back to the built-in default.
func (s *Store) Int(key string) int {
	k, raw, ok := s.lookup(key)
	if !ok {
		return 0
	}
	v, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		slog.Warn("settings: stored value is not a whole number, using the default", "key", key, "value", raw)
		v, _ = strconv.Atoi(k.Extract(s.baseline))
	}
	return v
}

// String returns the effective value of a string key.
func (s *Store) String(key string) string {
	_, raw, ok := s.lookup(key)
	if !ok {
		return ""
	}
	return raw
}

// StringList returns the effective value of a list key.
func (s *Store) StringList(key string) []string {
	_, raw, ok := s.lookup(key)
	if !ok {
		return nil
	}
	return parseList(raw)
}

// Set validates value, stores it as the override for key and republishes the
// cache. The order is deliberate: validate, commit to the database, then reload,
// so the database stays the source of truth.
func (s *Store) Set(key, value, updatedBy string) error {
	if s == nil {
		return errors.New("settings: store not initialised")
	}
	k, err := editable(key)
	if err != nil {
		return err
	}
	value = strings.TrimSpace(value)
	if k.Validate != nil {
		if err := k.Validate(value); err != nil {
			return fmt.Errorf("%s %w", key, err)
		}
	}

	// The lock spans validation, not just the write: validation reads the stored
	// overrides, so two concurrent writes could each validate against the
	// pre-change state and together persist a combination cfg.Validate rejects.
	// It is never held across a Discord or network call, only across this
	// process's own SQLite write.
	s.mu.Lock()
	defer s.mu.Unlock()

	// Validate against the same rules the boot path uses, on a merged copy: the
	// deploy-time config with every stored override applied, then this one on
	// top. Applying all overrides first means a combination of keys is checked
	// together (for example tools enabled plus a zero iteration cap), not just
	// this key against the file.
	merged := *s.base
	for _, other := range registry {
		if other.Name == k.Name {
			continue
		}
		v, ok := s.override(other.Name)
		if !ok {
			continue
		}
		if err := other.Apply(&merged, v); err != nil {
			return fmt.Errorf("settings: stored override for %s is invalid: %w", other.Name, err)
		}
	}
	if err := k.Apply(&merged, value); err != nil {
		return fmt.Errorf("%s %w", key, err)
	}
	if err := cfg.Validate(&merged); err != nil {
		return err
	}

	row := Setting{Key: k.Name, Value: value, UpdatedBy: updatedBy, UpdatedAt: time.Now().UTC()}
	if err := s.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{"value", "updated_by", "updated_at"}),
	}).Create(&row).Error; err != nil {
		return fmt.Errorf("settings: store %s: %w", key, err)
	}
	return s.reload()
}

// Unset removes the override for key, so the config.yaml value applies again.
func (s *Store) Unset(key, updatedBy string) error {
	if s == nil {
		return errors.New("settings: store not initialised")
	}
	k, err := editable(key)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	result := s.db.Where("key = ?", k.Name).Delete(&Setting{})
	if result.Error != nil {
		return fmt.Errorf("settings: delete %s: %w", key, result.Error)
	}
	if result.RowsAffected > 0 {
		slog.Info("settings: runtime override removed", "key", k.Name, "updated_by", updatedBy)
	}
	return s.reload()
}

// editable resolves a key that may be written, rejecting an unknown or secret
// name with ErrUnknownKey.
func editable(key string) (Key, error) {
	k, ok := byName[key]
	if !ok || k.Secret {
		return Key{}, fmt.Errorf("%w: %q", ErrUnknownKey, key)
	}
	return k, nil
}
