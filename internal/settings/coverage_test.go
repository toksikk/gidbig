package settings

import (
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/toksikk/gidbig/internal/cfg"
)

// unregisteredConfigKeys are cfg.Config leaves deliberately absent from the
// registry: credentials, which must never be listed, and the deprecated
// database.path, which no longer does anything.
var unregisteredConfigKeys = map[string]string{
	"discord.token":           "secret",
	"web.session_secret":      "secret",
	"web.oauth.client_secret": "secret",
	"llm.api_key":             "secret",
	"tmdb.read_access_token":  "secret",
	"database.path":           "deprecated, ignored",
}

// configLeaves returns the dotted yaml path of every non-struct field.
func configLeaves(t reflect.Type, prefix string) []string {
	var out []string
	for i := range t.NumField() {
		f := t.Field(i)
		name := strings.Split(f.Tag.Get("yaml"), ",")[0]
		if name == "" || name == "-" {
			continue
		}
		path := prefix + name
		if f.Type.Kind() == reflect.Struct {
			out = append(out, configLeaves(f.Type, path+".")...)
			continue
		}
		out = append(out, path)
	}
	return out
}

// TestRegistry_CoversEveryConfigField fails when a cfg.Config field is neither
// registered (hot or cold) nor listed in unregisteredConfigKeys, so a new
// config section cannot silently miss /admin config and the web settings panel.
func TestRegistry_CoversEveryConfigField(t *testing.T) {
	leaves := configLeaves(reflect.TypeFor[cfg.Config](), "")
	sort.Strings(leaves)
	seen := map[string]bool{}
	for _, leaf := range leaves {
		seen[leaf] = true
		_, registered := byName[leaf]
		_, excluded := unregisteredConfigKeys[leaf]
		switch {
		case registered && excluded:
			t.Errorf("%q is both registered and listed as unregistered", leaf)
		case !registered && !excluded:
			t.Errorf("config field %q is not in the settings registry: add a hot or cold key, or list it in unregisteredConfigKeys with a reason", leaf)
		}
	}
	for name := range byName {
		if !seen[name] {
			t.Errorf("registry key %q matches no cfg.Config field", name)
		}
	}
	for name := range unregisteredConfigKeys {
		if !seen[name] {
			t.Errorf("unregisteredConfigKeys entry %q matches no cfg.Config field", name)
		}
	}
}

func TestTVKeys_ColdWithEffectiveValues(t *testing.T) {
	store := newTestStore(t, openTestDB(t), baseYAML)
	for key, want := range map[string]string{
		"tv.enabled":          "false",
		"tv.channels":         "",
		"tv.cache_dir":        cfg.DefaultTVCacheDir,
		"tv.refresh_interval": cfg.DefaultTVRefreshInterval.String(),
		"logs.window_lines":   strconv.Itoa(cfg.DefaultLogWindowLines),
	} {
		k, ok := Lookup(key)
		if !ok || !k.Cold {
			t.Errorf("Lookup(%q) = %+v, %v; want a cold key", key, k, ok)
			continue
		}
		value, source, ok := store.Effective(key)
		if !ok || value != want || source != SourceDefault {
			t.Errorf("Effective(%q) = %q, %q, %v; want %q from default", key, value, source, ok, want)
		}
	}
}
