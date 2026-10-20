package settings

import (
	"strings"
	"testing"
)

func TestPersonalityKeys_AreHot(t *testing.T) {
	for _, name := range []string{KeyLLMPersonality, KeyLLMPersonalityPreset} {
		k, ok := Lookup(name)
		if !ok {
			t.Fatalf("%s is not registered", name)
		}
		if !k.Hot || k.Cold {
			t.Errorf("%s Hot=%v Cold=%v, want hot", name, k.Hot, k.Cold)
		}
	}
	if k, _ := Lookup(KeyLLMPersonality); k.Kind != KindText {
		t.Errorf("%s kind = %q, want %q", KeyLLMPersonality, k.Kind, KindText)
	}
}

func TestPersonalityPreset_UnknownIsRejected(t *testing.T) {
	db := openTestDB(t)
	store := newTestStore(t, db, baseYAML)
	err := store.Set(KeyLLMPersonalityPreset, "nope", "owner")
	if err == nil {
		t.Fatal("Set(nope) = nil, want rejection")
	}
	if !strings.Contains(err.Error(), "hal") {
		t.Errorf("error %q does not list the valid presets", err)
	}
	if _, ok := store.Override(KeyLLMPersonalityPreset); ok {
		t.Error("a rejected preset left an override")
	}
	var n int64
	db.Model(&Setting{}).Where("key = ?", KeyLLMPersonalityPreset).Count(&n)
	if n != 0 {
		t.Errorf("rows = %d, want 0", n)
	}
	if err := store.Set(KeyLLMPersonalityPreset, "dry", "owner"); err != nil {
		t.Fatalf("Set(dry): %v", err)
	}
	if v, ok := store.Override(KeyLLMPersonalityPreset); !ok || v != "dry" {
		t.Errorf("Override = %q, %v; want dry", v, ok)
	}
}

func TestPersonality_MultiLineRoundTrip(t *testing.T) {
	store := newTestStore(t, openTestDB(t), baseYAML)
	text := "line one\nline two\n\nline <four> & \"five\""
	if err := store.Set(KeyLLMPersonality, text, "owner"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	v, src, ok := store.Effective(KeyLLMPersonality)
	if !ok || v != text || src != SourceDB {
		t.Errorf("Effective = (%q, %q, %v), want the stored text from db", v, src, ok)
	}
	if err := store.Unset(KeyLLMPersonality, "owner"); err != nil {
		t.Fatalf("Unset: %v", err)
	}
	if _, ok := store.Override(KeyLLMPersonality); ok {
		t.Error("override still present after Unset")
	}
}

func TestKindText_ParsesLikeString(t *testing.T) {
	k := Key{Name: "x", Kind: KindText}
	for _, v := range []string{"", "a", "a\nb", "1,2"} {
		if !applicable(k, v) {
			t.Errorf("applicable(text, %q) = false", v)
		}
	}
}
