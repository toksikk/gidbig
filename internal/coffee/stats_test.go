package coffee

import (
	"context"
	"testing"
	"time"
)

func TestStats(t *testing.T) {
	m := newTestModule(t)
	db := m.getDB()
	for _, e := range []DrinkEvent{{GuildID: "g", UserID: "a", Drink: "espresso"}, {GuildID: "g", UserID: "a", Drink: "latte"}, {GuildID: "g", UserID: "b", Drink: "tea"}} {
		if err := db.Create(&e).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&DrinkOrder{GuildID: "g", UserID: "a", Drink: "x", Status: orderStatusReady}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&DrinkOrder{GuildID: "g", UserID: "b", Drink: "x", Status: orderStatusPickedUp}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&BrewRestriction{UserID: "b", Stage: 1, CycleStartedAt: time.Now(), BlockedUntil: time.Now().Add(time.Hour)}).Error; err != nil {
		t.Fatal(err)
	}

	st, err := m.Stats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, s := range append(st.Summary, st.Detail...) {
		got[s.Name] = s.Value
	}
	want := map[string]string{"cups": "3", "drinkers": "2", "open orders": "1", "blocked users": "1", "refills": "0"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}

func TestStatsWithoutStore(t *testing.T) {
	if _, err := New().Stats(context.Background()); err == nil {
		t.Fatal("expected error without store")
	}
}

func TestStatsHonoursCancelledContext(t *testing.T) {
	m := newTestModule(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := m.Stats(ctx); err == nil {
		t.Fatal("expected error for cancelled context")
	}
}
