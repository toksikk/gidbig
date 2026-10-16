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

// ErrRestartRequired is returned by every write to a cold key: it is known and
// readable, but only config.yaml and a restart change it.
var ErrRestartRequired = errors.New("read-only at runtime; restart required: edit config.yaml")

// ErrNotList is returned by Add and Remove for a key that is not a list.
var ErrNotList = errors.New("not a list setting; use set instead")

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
	// invalid is non-nil when the configuration these values produce fails
	// cfg.Validate. A row that was valid when it was written can be invalidated
	// by a later config.yaml edit, so the combination is checked on every load
	// and not only when a value is written.
	invalid error
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
	if err := s.EffectiveError(); err != nil {
		// Start anyway: refusing to boot would leave the owner with no way to
		// clear the row from Discord. The values stay in force, `/admin config
		// list` reports the problem, and every `set` is rejected until the
		// combination is valid again -- `unset` stays allowed so it can be.
		slog.Error("settings: the runtime overrides make the effective configuration invalid; fix it with /admin config",
			"error", err)
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
			slog.Warn("settings: ignoring a stored value for an unknown or non-editable key", "key", row.Key)
			continue
		}
		if k.Cold {
			slog.Warn("settings: ignoring a stored value for a cold key; edit config.yaml instead", "key", row.Key)
			continue
		}
		values[row.Key] = row.Value
	}
	s.now.Store(&snapshot{values: values, invalid: s.combinationError(values)})
	return nil
}

// combinationError reports whether the configuration a set of stored values
// produces would pass cfg.Validate. It exists because a stored row is validated
// against the config.yaml of the day it was written: a later deployment edit can
// leave the two jointly invalid (tools enabled in the file, a zero iteration cap
// in a row), which no single write could have produced.
func (s *Store) combinationError(values map[string]string) error {
	merged := *s.base
	for _, k := range registry {
		v, ok := values[k.Name]
		if !ok || k.Apply == nil || !applicable(k, v) {
			continue
		}
		if err := k.Apply(&merged, v); err != nil {
			continue
		}
	}
	return cfg.Validate(&merged)
}

// EffectiveError returns non-nil when the overrides in force produce a
// configuration cfg.Validate rejects. The values are still published in that
// case — the owner has to be able to see and fix them — so `/admin config list`
// surfaces this, every `set` is rejected until the combination is valid again,
// and `unset` stays allowed because removing a row is how it gets fixed.
func (s *Store) EffectiveError() error {
	if s == nil {
		return nil
	}
	snap := s.now.Load()
	if snap == nil {
		return nil
	}
	return snap.invalid
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

// OverrideInt returns the stored override for an integer key, parsed. It reports
// false when no row exists or the stored value is not a whole number, so a
// malformed row cannot disable the caller's own fallback.
func (s *Store) OverrideInt(key string) (int, bool) {
	v, ok := s.Override(key)
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return 0, false
	}
	return n, true
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
		if applicable(k, v) {
			return v, SourceDB, true
		}
		// A row that does not parse, or fails the key's own rule, is not applied
		// by the typed getters either. Reporting it as db would show a value
		// nothing uses, so fall through to the value actually in force.
		slog.Warn("settings: stored override is not usable, reporting the config value instead",
			"key", key, "value", v)
	}
	v := k.Extract(s.base)
	if v == k.Extract(s.baseline) {
		return v, SourceDefault, true
	}
	return v, SourceYAML, true
}

// applicable reports whether a stored value would really be applied for its key:
// it must parse for the key's kind and pass the key's own rule. This mirrors what
// the typed getters do on read, so "source: db" always means "this is the value
// in force".
func applicable(k Key, value string) bool {
	switch k.Kind {
	case KindBool:
		if _, err := strconv.ParseBool(strings.TrimSpace(value)); err != nil {
			return false
		}
	case KindInt:
		if _, err := strconv.Atoi(strings.TrimSpace(value)); err != nil {
			return false
		}
	case KindString, KindStringList:
		// Any string is usable; a list is split leniently.
	}
	if k.Validate != nil && k.Validate(value) != nil {
		return false
	}
	return true
}

// lookup returns the effective raw value for a known, non-secret key. A stored
// row is used only when it is applicable, so the typed getters and Effective can
// never disagree about which value is in force. The fallback is silent here
// because lookup runs on every message; Effective logs it for the owner, and
// reload warns once per start about rows it does not recognise.
func (s *Store) lookup(key string) (Key, string, bool) {
	if s == nil {
		return Key{}, "", false
	}
	k, ok := byName[key]
	if !ok || k.Secret {
		return Key{}, "", false
	}
	if v, ok := s.override(key); ok && applicable(k, v) {
		return k, v, true
	}
	return k, k.Extract(s.base), true
}

// Bool returns the effective value of a boolean key. A stored value that is not
// usable falls back to the config.yaml value.
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

// Int returns the effective value of an integer key. A stored value that is not
// usable falls back to the config.yaml value.
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
	// The lock spans validation, not just the write: validation reads the stored
	// overrides, so two concurrent writes could each validate against the
	// pre-change state and together persist a combination cfg.Validate rejects.
	// It is never held across a Discord or network call, only across this
	// process's own SQLite write.
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.setLocked(k, value, updatedBy)
}

// Add appends item to a list key and stores the result like Set. An item that is
// already present is rejected and nothing is written.
func (s *Store) Add(key, item, updatedBy string) error {
	return s.editList(key, item, updatedBy, func(list []string, item string) ([]string, error) {
		for _, existing := range list {
			if existing == item {
				return nil, fmt.Errorf("%s already contains %q", key, item)
			}
		}
		return append(list, item), nil
	})
}

// Remove drops item from a list key and stores the result like Set. An item that
// is not present is rejected and nothing is written.
func (s *Store) Remove(key, item, updatedBy string) error {
	return s.editList(key, item, updatedBy, func(list []string, item string) ([]string, error) {
		for i, existing := range list {
			if existing == item {
				return append(list[:i:i], list[i+1:]...), nil
			}
		}
		return nil, fmt.Errorf("%s does not contain %q", key, item)
	})
}

// editList reads the effective list under the write lock, edits it and stores
// it through the same validation path as Set, so a concurrent write cannot be
// lost between the read and the write.
func (s *Store) editList(key, item, updatedBy string, edit func([]string, string) ([]string, error)) error {
	if s == nil {
		return errors.New("settings: store not initialised")
	}
	k, err := editable(key)
	if err != nil {
		return err
	}
	if k.Kind != KindStringList {
		return fmt.Errorf("%w: %q", ErrNotList, key)
	}
	item = strings.TrimSpace(item)
	if item == "" || strings.ContainsAny(item, ",\n") {
		return fmt.Errorf("%s item must be one non-empty value without commas", key)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := edit(s.StringList(key), item)
	if err != nil {
		return err
	}
	return s.setLocked(k, formatList(list), updatedBy)
}

// setLocked validates and stores value for k. The caller holds s.mu.
func (s *Store) setLocked(k Key, value, updatedBy string) error {
	key := k.Name
	value = strings.TrimSpace(value)
	if k.Validate != nil {
		if err := k.Validate(value); err != nil {
			return fmt.Errorf("%s %w", key, err)
		}
	}

	// Validate against the same rules the boot path uses, on a merged copy: the
	// deploy-time config with every stored override applied, then this one on
	// top. Applying all overrides first means a combination of keys is checked
	// together (for example tools enabled plus a zero iteration cap), not just
	// this key against the file.
	merged := *s.base
	for _, other := range registry {
		if other.Name == k.Name || other.Apply == nil {
			continue
		}
		v, ok := s.override(other.Name)
		if !ok {
			continue
		}
		if !applicable(other, v) {
			// The read path ignores this row as well, so it must not decide
			// whether an unrelated key may be written.
			slog.Warn("settings: ignoring a stored override that is not usable while validating",
				"key", other.Name, "value", v)
			continue
		}
		if err := other.Apply(&merged, v); err != nil {
			// The read path ignores a row it cannot parse, so skipping it here
			// keeps the merged config equal to the effective one instead of
			// letting one corrupt row block every unrelated write. The error is
			// still surfaced, and `unset` clears the row.
			slog.Warn("settings: ignoring an unusable stored override while validating",
				"key", other.Name, "error", err)
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

	// Removing a row changes the effective configuration, so it gets the same
	// check a write does. Only an unset that would *introduce* an invalid
	// combination is refused, so an already-invalid state can always be cleared,
	// and setting a valid value first always works.
	remaining := make(map[string]string, len(registry))
	if snap := s.now.Load(); snap != nil {
		for stored, v := range snap.values {
			remaining[stored] = v
		}
	}
	delete(remaining, k.Name)
	if err := s.combinationError(remaining); err != nil && s.EffectiveError() == nil {
		return fmt.Errorf("settings: would leave the configuration invalid: %w", err)
	}

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
// name with ErrUnknownKey and a cold one with ErrRestartRequired.
func editable(key string) (Key, error) {
	k, ok := byName[key]
	if !ok || k.Secret {
		return Key{}, fmt.Errorf("%w: %q", ErrUnknownKey, key)
	}
	if k.Cold {
		return Key{}, fmt.Errorf("%q is %w", key, ErrRestartRequired)
	}
	return k, nil
}
