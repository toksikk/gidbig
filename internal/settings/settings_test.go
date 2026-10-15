package settings

import (
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/toksikk/gidbig/internal/cfg"
	"github.com/toksikk/gidbig/internal/shareddb"
	"gorm.io/gorm"
)

// baseYAML is the minimum config the boot path accepts: a token and a
// non-empty allowed_guilds list. Everything else takes the built-in default.
const baseYAML = `
discord:
  token: "test-token"
gippity:
  allowed_guilds: ["guild"]
`

// openTestDB opens the same shared-database configuration production uses, so
// the single-connection pool and WAL settings are exercised too.
func openTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := shareddb.Open(filepath.Join(t.TempDir(), shareddb.Filename))
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func newTestStore(t *testing.T, db *gorm.DB, yaml string) *Store {
	t.Helper()
	base, err := cfg.Decode(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("decode base config: %v", err)
	}
	store, err := New(db, base)
	if err != nil {
		t.Fatalf("settings.New: %v", err)
	}
	return store
}

func TestNew_EmptyStoreReportsDefaults(t *testing.T) {
	store := newTestStore(t, openTestDB(t), baseYAML)

	value, source, ok := store.Effective(KeySoundboardQueueMaxDepth)
	if !ok {
		t.Fatal("queue_max_depth is not a known key")
	}
	if value != "6" || source != SourceDefault {
		t.Errorf("Effective(queue_max_depth) = (%q, %q), want (\"6\", %q)", value, source, SourceDefault)
	}
	if got := store.Int(KeySoundboardQueueMaxDepth); got != 6 {
		t.Errorf("Int(queue_max_depth) = %d, want 6", got)
	}

	// A key whose default lives only in the module and in cfg must report that
	// default, not zero: the owner would otherwise see a limit that would reject
	// every classifier call.
	value, source, _ = store.Effective(KeyGippityClassifierRateLimit)
	if value != "30" || source != SourceDefault {
		t.Errorf("Effective(classifier_rate_limit_per_hour) = (%q, %q), want (\"30\", %q)", value, source, SourceDefault)
	}
}

func TestEffective_PrecedenceDefaultYAMLDB(t *testing.T) {
	const yamlConfig = `
discord:
  token: "test-token"
gippity:
  allowed_guilds: ["guild"]
soundboard:
  queue_max_depth: 9
`
	db := openTestDB(t)
	store := newTestStore(t, db, yamlConfig)

	value, source, _ := store.Effective(KeySoundboardQueueMaxDepth)
	if value != "9" || source != SourceYAML {
		t.Fatalf("before Set: Effective = (%q, %q), want (\"9\", %q)", value, source, SourceYAML)
	}

	if err := store.Set(KeySoundboardQueueMaxDepth, "12", "owner"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	value, source, _ = store.Effective(KeySoundboardQueueMaxDepth)
	if value != "12" || source != SourceDB {
		t.Fatalf("after Set: Effective = (%q, %q), want (\"12\", %q)", value, source, SourceDB)
	}

	if err := store.Unset(KeySoundboardQueueMaxDepth, "owner"); err != nil {
		t.Fatalf("Unset: %v", err)
	}
	value, source, _ = store.Effective(KeySoundboardQueueMaxDepth)
	if value != "9" || source != SourceYAML {
		t.Fatalf("after Unset: Effective = (%q, %q), want (\"9\", %q)", value, source, SourceYAML)
	}
}

// TestEffective_YAMLValueEqualToDefault pins the documented ambiguity: a
// config.yaml value identical to the built-in default is reported as the
// default, because the effective value is the same either way.
func TestEffective_YAMLValueEqualToDefault(t *testing.T) {
	const yamlConfig = `
discord:
  token: "test-token"
gippity:
  allowed_guilds: ["guild"]
soundboard:
  queue_max_depth: 6
`
	store := newTestStore(t, openTestDB(t), yamlConfig)
	if _, source, _ := store.Effective(KeySoundboardQueueMaxDepth); source != SourceDefault {
		t.Errorf("source = %q, want %q", source, SourceDefault)
	}
}

func TestSet_UnknownAndNonEditableKeysAreRejected(t *testing.T) {
	store := newTestStore(t, openTestDB(t), baseYAML)
	keys := []string{
		"nope",
		"",
		"discord.token",
		"web.port",
		"web.session_secret",
		"dev_mode",
		"leetoclock.debug",
		"llm.provider",
		"gippity.allowed_guilds",
	}
	for _, key := range keys {
		if err := store.Set(key, "value", "owner"); err == nil {
			t.Errorf("Set(%q) = nil, want %v", key, ErrUnknownKey)
		} else if !strings.Contains(err.Error(), ErrUnknownKey.Error()) {
			t.Errorf("Set(%q) error = %v, want %v", key, err, ErrUnknownKey)
		}
		if err := store.Unset(key, "owner"); err == nil {
			t.Errorf("Unset(%q) = nil, want %v", key, ErrUnknownKey)
		}
		if _, _, ok := store.Effective(key); ok {
			t.Errorf("Effective(%q) reported the key as known", key)
		}
	}
	if _, ok := store.Override("discord.token"); ok {
		t.Error("Override returned a value for a non-registered key")
	}
}

// TestSecretKeysAreNeverExposed registers a temporary secret key to prove the
// guard: a secret name must behave exactly like an unknown one everywhere the
// command can reach. Nothing in the shipping registry is secret; the flag exists
// so a future addition cannot leak a credential.
func TestSecretKeysAreNeverExposed(t *testing.T) {
	const name = "test.secret"
	key := Key{
		Name:    name,
		Kind:    KindString,
		Hot:     true,
		Secret:  true,
		Extract: func(*cfg.Config) string { return "secret-value" },
		Apply:   func(*cfg.Config, string) error { return nil },
	}
	registry = append(registry, key)
	byName[name] = key
	t.Cleanup(func() {
		registry = registry[:len(registry)-1]
		delete(byName, name)
	})

	store := newTestStore(t, openTestDB(t), baseYAML)
	if err := store.db.Create(&Setting{Key: name, Value: "leaked", UpdatedBy: "manual"}).Error; err != nil {
		t.Fatalf("insert secret row: %v", err)
	}
	if err := store.reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}

	if _, ok := Lookup(name); ok {
		t.Error("Lookup exposed a secret key")
	}
	for _, k := range Keys() {
		if k.Name == name {
			t.Error("Keys exposed a secret key")
		}
	}
	if _, _, ok := store.Effective(name); ok {
		t.Error("Effective exposed a secret key")
	}
	if _, ok := store.Override(name); ok {
		t.Error("Override exposed a secret key")
	}
	if got := store.String(name); got != "" {
		t.Errorf("String(secret) = %q, want empty", got)
	}
	if err := store.Set(name, "x", "owner"); !errors.Is(err, ErrUnknownKey) {
		t.Errorf("Set(secret) error = %v, want %v", err, ErrUnknownKey)
	}
	if err := store.Unset(name, "owner"); !errors.Is(err, ErrUnknownKey) {
		t.Errorf("Unset(secret) error = %v, want %v", err, ErrUnknownKey)
	}
}

// TestRegistry_NoSecretKeys guards the invariant that no credential is ever
// reachable from /admin config. It walks the raw registry (not Keys(), which
// filters secrets) so a future secret addition fails loudly here.
func TestRegistry_NoSecretKeys(t *testing.T) {
	for _, k := range registry {
		if k.Secret {
			t.Errorf("key %q is registered as secret", k.Name)
		}
		if !k.Hot {
			t.Errorf("key %q is not hot; only hot keys may be registered", k.Name)
		}
		if k.Extract == nil || k.Apply == nil {
			t.Errorf("key %q is missing Extract or Apply", k.Name)
		}
	}
}

func TestSet_RejectsInvalidValuesWithoutPersisting(t *testing.T) {
	tests := []struct {
		name      string
		key       string
		value     string
		wantError string
	}{
		{"bad enum", KeyGippityToolsReasoningEffort, "bogus", "reasoning_effort"},
		{"non-positive rate limit", KeyGippityRateLimitMessagesPerHour, "0", "rate_limit_messages_per_hour"},
		{"non-numeric rate limit", KeyGippityRateLimitMessagesPerHour, "many", "whole number"},
		{"non-positive classifier limit", KeyGippityClassifierRateLimit, "0", "greater than zero"},
		{"non-positive queue depth", KeySoundboardQueueMaxDepth, "-2", "queue_max_depth"},
		{"empty model", KeyLLMModel, "", "must not be empty"},
		{"bad boolean", KeyGippityToolsEnabled, "yes", "true or false"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newTestStore(t, openTestDB(t), baseYAML)
			before, beforeSource, _ := store.Effective(tt.key)

			err := store.Set(tt.key, tt.value, "owner")
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("Set(%s=%q) error = %v, want error containing %q", tt.key, tt.value, err, tt.wantError)
			}
			if _, alreadySet := store.Override(tt.key); alreadySet {
				t.Fatalf("a rejected value was persisted for %s", tt.key)
			}
			after, afterSource, _ := store.Effective(tt.key)
			if after != before || afterSource != beforeSource {
				t.Errorf("effective value changed from (%q, %q) to (%q, %q)", before, beforeSource, after, afterSource)
			}
		})
	}
}

// TestSet_ValidatesCombinedOverrides proves a stored override is part of the
// validation input: two values that are individually fine can still be invalid
// together, exactly as at boot.
func TestSet_ValidatesCombinedOverrides(t *testing.T) {
	store := newTestStore(t, openTestDB(t), baseYAML)

	// Zero tool iterations is accepted while tools are off (the config rule
	// only applies when tools are enabled).
	if err := store.Set(KeyGippityToolsMaxIterations, "0", "owner"); err != nil {
		t.Fatalf("Set(max_iterations=0) with tools off: %v", err)
	}
	// Enabling tools must now be rejected, because the stored 0 is in effect.
	err := store.Set(KeyGippityToolsEnabled, "true", "owner")
	if err == nil || !strings.Contains(err.Error(), "max_iterations") {
		t.Fatalf("Set(tools.enabled=true) error = %v, want an error naming max_iterations", err)
	}
	if _, set := store.Override(KeyGippityToolsEnabled); set {
		t.Error("tools.enabled was persisted despite the rejection")
	}
}

func TestNew_ReloadsStoredOverrides(t *testing.T) {
	db := openTestDB(t)
	first := newTestStore(t, db, baseYAML)
	if err := first.Set(KeyLLMModel, "runtime-model", "owner"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// A fresh store over the same database stands in for a restart.
	second := newTestStore(t, db, baseYAML)
	value, source, ok := second.Effective(KeyLLMModel)
	if !ok || value != "runtime-model" || source != SourceDB {
		t.Fatalf("after reload: Effective(llm.model) = (%q, %q, %v), want (\"runtime-model\", %q, true)", value, source, ok, SourceDB)
	}
}

func TestOverride_IsRawOperatorIntent(t *testing.T) {
	const yamlConfig = `
discord:
  token: "test-token"
gippity:
  allowed_guilds: ["guild"]
llm:
  model: "yaml-model"
`
	store := newTestStore(t, openTestDB(t), yamlConfig)
	if _, ok := store.Override(KeyLLMModel); ok {
		t.Error("Override reported a row before anything was set")
	}
	if got := store.String(KeyLLMModel); got != "yaml-model" {
		t.Errorf("String(llm.model) = %q, want %q", got, "yaml-model")
	}
	if err := store.Set(KeyLLMModel, "runtime-model", "owner"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if got, ok := store.Override(KeyLLMModel); !ok || got != "runtime-model" {
		t.Errorf("Override(llm.model) = (%q, %v), want (\"runtime-model\", true)", got, ok)
	}
}

func TestStringList_RoundTripsCommaSeparatedValues(t *testing.T) {
	store := newTestStore(t, openTestDB(t), baseYAML)
	if got := store.StringList(KeyGippityToolsAllowed); len(got) != 0 {
		t.Errorf("empty list = %v, want none", got)
	}
	if err := store.Set(KeyGippityToolsAllowed, "bot_status, weather\nleetoclock_records", "owner"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got := store.StringList(KeyGippityToolsAllowed)
	want := []string{"bot_status", "weather", "leetoclock_records"}
	if len(got) != len(want) {
		t.Fatalf("StringList = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("StringList = %v, want %v", got, want)
		}
	}
}

// TestTypedGetters_FallBackOnMalformedRow covers a row written outside the
// command (for example by hand with sqlite3): it must not crash a hot path.
func TestTypedGetters_FallBackOnMalformedRow(t *testing.T) {
	db := openTestDB(t)
	store := newTestStore(t, db, baseYAML)
	if err := db.Create(&Setting{Key: KeySoundboardQueueMaxDepth, Value: "not-a-number", UpdatedBy: "manual"}).Error; err != nil {
		t.Fatalf("insert malformed row: %v", err)
	}
	if err := store.reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := store.Int(KeySoundboardQueueMaxDepth); got != 6 {
		t.Errorf("Int(queue_max_depth) = %d, want the default 6", got)
	}
}

func TestNew_RejectsNilInputs(t *testing.T) {
	if _, err := New(nil, &cfg.Config{}); err == nil {
		t.Error("New(nil db) = nil, want an error")
	}
	if _, err := New(openTestDB(t), nil); err == nil {
		t.Error("New(nil base) = nil, want an error")
	}
}

// TestNilStore_ReturnsZeroValues covers the guarded branches: a module that
// somehow reached a getter with a nil store must get the zero value rather than
// a panic.
func TestNilStore_ReturnsZeroValues(t *testing.T) {
	var store *Store

	if store.Bool(KeyGippityToolsEnabled) {
		t.Error("nil store returned a non-zero bool")
	}
	if got := store.Int(KeySoundboardQueueMaxDepth); got != 0 {
		t.Errorf("nil store Int = %d, want 0", got)
	}
	if got := store.String(KeyLLMModel); got != "" {
		t.Errorf("nil store String = %q, want empty", got)
	}
	if got := store.StringList(KeyGippityToolsAllowed); got != nil {
		t.Errorf("nil store StringList = %v, want nil", got)
	}
	if _, ok := store.Override(KeyLLMModel); ok {
		t.Error("nil store Override reported a value")
	}
	if _, _, ok := store.Effective(KeyLLMModel); ok {
		t.Error("nil store Effective reported a key")
	}
	if err := store.Set(KeyLLMModel, "x", "owner"); err == nil {
		t.Error("nil store Set = nil, want an error")
	}
	if err := store.Unset(KeyLLMModel, "owner"); err == nil {
		t.Error("nil store Unset = nil, want an error")
	}
}

// TestConcurrentReadsAndWrites exercises the atomic cache swap: readers must
// never see a half-built snapshot, and Set must not need a read lock.
func TestConcurrentReadsAndWrites(t *testing.T) {
	store := newTestStore(t, openTestDB(t), baseYAML)

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = store.Int(KeySoundboardQueueMaxDepth)
					_ = store.Bool(KeyGippityToolsEnabled)
					_ = store.String(KeyLLMModel)
					_ = store.StringList(KeyGippityToolsAllowed)
				}
			}
		}()
	}

	for i := 0; i < 20; i++ {
		value := "4"
		if i%2 == 1 {
			value = "5"
		}
		if err := store.Set(KeySoundboardQueueMaxDepth, value, "owner"); err != nil {
			t.Fatalf("Set: %v", err)
		}
	}
	close(stop)
	wg.Wait()
}

func TestOverrideInt(t *testing.T) {
	store := newTestStore(t, openTestDB(t), baseYAML)

	if _, ok := store.OverrideInt(KeyLLMMaxTokens); ok {
		t.Error("OverrideInt reported a value before anything was set")
	}
	if err := store.Set(KeyLLMMaxTokens, "700", "owner"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if got, ok := store.OverrideInt(KeyLLMMaxTokens); !ok || got != 700 {
		t.Errorf("OverrideInt = (%d, %v), want (700, true)", got, ok)
	}

	// A row written outside the command must not disable the caller's fallback.
	if err := store.db.Exec(
		"UPDATE settings SET value = ? WHERE key = ?", "not-a-number", KeyLLMMaxTokens,
	).Error; err != nil {
		t.Fatalf("corrupt row: %v", err)
	}
	if err := store.reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if _, ok := store.OverrideInt(KeyLLMMaxTokens); ok {
		t.Error("OverrideInt accepted a malformed row")
	}
	if _, ok := store.OverrideInt("discord.token"); ok {
		t.Error("OverrideInt reported a value for a non-registered key")
	}
}

// TestEffective_LLMMaxTokensDefault pins that the completion cap reports the
// shared 300 rather than zero, which would look like a disabled completion.
func TestEffective_LLMMaxTokensDefault(t *testing.T) {
	store := newTestStore(t, openTestDB(t), baseYAML)
	value, source, ok := store.Effective(KeyLLMMaxTokens)
	if !ok {
		t.Fatal("llm.max_tokens is not a known key")
	}
	if value != "300" || source != SourceDefault {
		t.Errorf("Effective(llm.max_tokens) = (%q, %q), want (\"300\", %q)", value, source, SourceDefault)
	}
}

// TestEffective_UnusableRowIsNotReportedAsInForce pins that "source: db" always
// means "this is the value in force": a row that does not parse, or that fails
// the key's own rule, is reported as the config value instead.
func TestEffective_UnusableRowIsNotReportedAsInForce(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
	}{
		{"unparseable int", KeySoundboardQueueMaxDepth, "not-a-number"},
		{"unparseable bool", KeyGippityToolsEnabled, "yes"},
		{"int failing the key rule", KeySoundboardQueueMaxDepth, "-1"},
		{"string failing the key rule", KeyLLMModel, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newTestStore(t, openTestDB(t), baseYAML)
			// Written outside Set, which would have rejected all four.
			if err := store.db.Create(&Setting{Key: tt.key, Value: tt.value, UpdatedBy: "manual"}).Error; err != nil {
				t.Fatalf("insert row: %v", err)
			}
			if err := store.reload(); err != nil {
				t.Fatalf("reload: %v", err)
			}

			value, source, ok := store.Effective(tt.key)
			if !ok {
				t.Fatalf("Effective(%s) reported an unknown key", tt.key)
			}
			if source == SourceDB {
				t.Errorf("Effective(%s) = (%q, %q): an unusable row was reported as in force", tt.key, value, source)
			}
			// The reported value must match what the consumer's getter produces.
			var expected string
			switch {
			case strings.HasSuffix(tt.key, "enabled"):
				expected = strconv.FormatBool(store.Bool(tt.key))
			case strings.HasSuffix(tt.key, "depth"):
				expected = strconv.Itoa(store.Int(tt.key))
			default:
				expected = store.String(tt.key)
			}
			if value != expected {
				t.Errorf("Effective(%s) = %q, but the getter yields %q", tt.key, value, expected)
			}
		})
	}
}

// TestSet_SkipsAnUnusableRowInsteadOfBlocking pins that one corrupt row cannot
// stop unrelated keys from being written.
func TestSet_SkipsAnUnusableRowInsteadOfBlocking(t *testing.T) {
	store := newTestStore(t, openTestDB(t), baseYAML)
	if err := store.db.Create(&Setting{Key: KeySoundboardQueueMaxDepth, Value: "abc", UpdatedBy: "manual"}).Error; err != nil {
		t.Fatalf("insert row: %v", err)
	}
	if err := store.reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}

	if err := store.Set(KeyLLMModel, "runtime-model", "owner"); err != nil {
		t.Fatalf("Set on an unrelated key: %v", err)
	}
	if got := store.String(KeyLLMModel); got != "runtime-model" {
		t.Errorf("llm.model = %q, want the written value", got)
	}
}

// TestSet_SkipsARowThatFailsItsOwnRule pins the second half of the skip: a row
// that parses but would never be applied must not decide whether an unrelated key
// can be written. The combined-validation property still holds, because the row in
// TestSet_ValidatesCombinedOverrides is applicable at the time it is set.
func TestSet_SkipsARowThatFailsItsOwnRule(t *testing.T) {
	store := newTestStore(t, openTestDB(t), baseYAML)
	if err := store.db.Create(&Setting{Key: KeySoundboardQueueMaxDepth, Value: "-2", UpdatedBy: "manual"}).Error; err != nil {
		t.Fatalf("insert row: %v", err)
	}
	if err := store.reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if _, source, _ := store.Effective(KeySoundboardQueueMaxDepth); source == SourceDB {
		t.Fatal("an unusable row was reported as in force")
	}

	if err := store.Set(KeyLLMModel, "runtime-model", "owner"); err != nil {
		t.Fatalf("Set on an unrelated key: %v", err)
	}
	if got := store.String(KeyLLMModel); got != "runtime-model" {
		t.Errorf("llm.model = %q, want the written value", got)
	}
}

// TestEffective_EnumRowIsNotInForce covers the reasoning_effort enum, whose rule
// lives in cfg: a hand-edited unknown value must be reported as unusable rather
// than as a value in force, and must not block writes to other keys.
func TestEffective_EnumRowIsNotInForce(t *testing.T) {
	store := newTestStore(t, openTestDB(t), baseYAML)
	if err := store.db.Create(&Setting{Key: KeyGippityToolsReasoningEffort, Value: "bogus", UpdatedBy: "manual"}).Error; err != nil {
		t.Fatalf("insert row: %v", err)
	}
	if err := store.reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}

	value, source, ok := store.Effective(KeyGippityToolsReasoningEffort)
	if !ok {
		t.Fatal("reasoning_effort is not a known key")
	}
	if source == SourceDB {
		t.Errorf("Effective = (%q, %q): an unusable enum row was reported as in force", value, source)
	}
	if err := store.Set(KeyLLMModel, "runtime-model", "owner"); err != nil {
		t.Fatalf("Set on an unrelated key: %v", err)
	}
}

// TestEffective_EveryRegisteredKeyIsJudged pins the invariant for the whole
// registry: a row that Set would reject must never be reported as source db, and
// one Set accepts must be. A key whose rule exists only in cfg.Validate, or that
// loses its registry rule, fails here.
//
// The probe value is per-kind on purpose: a value that does not parse at all is
// rejected before any key rule is consulted, so it would never exercise the
// positive-value caps. "0" parses for an integer and is exactly what those rules
// exist to reject.
func TestEffective_EveryRegisteredKeyIsJudged(t *testing.T) {
	probe := map[Kind]string{
		KindInt:        "0",
		KindBool:       "not-a-bool",
		KindString:     "not-a-valid-value",
		KindStringList: "not-a-valid-value",
	}

	for _, k := range registry {
		t.Run(k.Name, func(t *testing.T) {
			value, ok := probe[k.Kind]
			if !ok {
				t.Fatalf("no probe value for kind %q", k.Kind)
			}

			store := newTestStore(t, openTestDB(t), baseYAML)
			if err := store.db.Create(&Setting{Key: k.Name, Value: value, UpdatedBy: "manual"}).Error; err != nil {
				t.Fatalf("insert row: %v", err)
			}
			if err := store.reload(); err != nil {
				t.Fatalf("reload: %v", err)
			}

			_, source, ok := store.Effective(k.Name)
			if !ok {
				t.Fatalf("Effective(%s) reported an unknown key", k.Name)
			}
			if err := store.Set(k.Name, value, "owner"); err == nil {
				// Set accepts the probe, so the row is a real value and must be
				// reported as in force.
				if source != SourceDB {
					t.Errorf("Set accepted %q but Effective reports source %q", value, source)
				}
				return
			}
			if source == SourceDB {
				t.Errorf("Set rejects %q but Effective reports it as source db", value)
			}
		})
	}
}

const baseToolsOnYAML = `
discord:
  token: "test-token"
gippity:
  allowed_guilds: ["guild"]
  tools:
    enabled: true
    max_iterations: 3
`

// TestNew_ReportsAnInvalidCombinedConfiguration covers the deferral window: a row
// valid when it was written can be invalidated by a later config.yaml edit.
func TestNew_ReportsAnInvalidCombinedConfiguration(t *testing.T) {
	db := openTestDB(t)
	store := newTestStore(t, db, baseYAML)
	if err := store.Set(KeyGippityToolsMaxIterations, "0", "owner"); err != nil {
		t.Fatalf("Set with tools off: %v", err)
	}

	restarted := newTestStore(t, db, baseToolsOnYAML)
	err := restarted.EffectiveError()
	if err == nil {
		t.Fatal("New published a configuration cfg.Validate rejects without reporting it")
	}
	if !strings.Contains(err.Error(), "max_iterations") {
		t.Errorf("EffectiveError = %v, want it to name the offending key", err)
	}
}

func TestUnset_RejectsRemovalThatInvalidatesTheConfiguration(t *testing.T) {
	store := newTestStore(t, openTestDB(t), baseToolsOnYAML)
	if err := store.Set(KeyGippityToolsEnabled, "false", "owner"); err != nil {
		t.Fatalf("Set tools.enabled: %v", err)
	}
	if err := store.Set(KeyGippityToolsMaxIterations, "0", "owner"); err != nil {
		t.Fatalf("Set max_iterations: %v", err)
	}

	err := store.Unset(KeyGippityToolsEnabled, "owner")
	if err == nil {
		t.Fatal("Unset accepted a removal that leaves tools enabled with zero iterations")
	}
	if !strings.Contains(err.Error(), "max_iterations") {
		t.Errorf("Unset error = %v, want it to name the conflicting key", err)
	}
	if v, ok := store.Override(KeyGippityToolsEnabled); !ok || v != "false" {
		t.Errorf("the rejected Unset changed the stored row: %q (present %v)", v, ok)
	}

	// There must always be a way out: set a valid cap, then remove the override.
	if err := store.Set(KeyGippityToolsMaxIterations, "3", "owner"); err != nil {
		t.Fatalf("Set a valid cap: %v", err)
	}
	if err := store.Unset(KeyGippityToolsEnabled, "owner"); err != nil {
		t.Fatalf("Unset after fixing the cap: %v", err)
	}
}
