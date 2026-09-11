package coffee

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"gorm.io/gorm"
)

func createReadyOrder(t *testing.T, m *Module, guildID, userID string, readyAt time.Time) DrinkOrder {
	t.Helper()
	order := DrinkOrder{GuildID: guildID, UserID: userID, Drink: "coffee", Status: orderStatusReady, ReadyAt: readyAt, ExpiresAt: readyAt.Add(pickupWindow)}
	if err := m.getDB().Create(&order).Error; err != nil {
		t.Fatalf("create order: %v", err)
	}
	return order
}

func recordViolation(t *testing.T, m *Module, orderID uint, userID string, at time.Time) {
	t.Helper()
	if err := m.getDB().Transaction(func(tx *gorm.DB) error {
		return recordPickupViolationTx(tx, orderID, userID, at)
	}); err != nil {
		t.Fatalf("record violation: %v", err)
	}
}

func loadRestriction(t *testing.T, m *Module, userID string) BrewRestriction {
	t.Helper()
	var state BrewRestriction
	if err := m.getDB().Where("user_id = ?", userID).First(&state).Error; err != nil {
		t.Fatalf("load restriction: %v", err)
	}
	return state
}

func TestExpireDueOrdersHonorsPickupDeadline(t *testing.T) {
	m := newTestModule(t)
	readyAt := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	order := createReadyOrder(t, m, "g1", "u1", readyAt)
	count, err := m.expireDueOrders(order.ExpiresAt.Add(-time.Nanosecond))
	if err != nil || count != 0 {
		t.Fatalf("before deadline: count=%d err=%v", count, err)
	}
	count, err = m.expireDueOrders(order.ExpiresAt)
	if err != nil || count != 1 {
		t.Fatalf("at deadline: count=%d err=%v", count, err)
	}
	var violations int64
	m.getDB().Model(&PickupViolation{}).Where("order_id = ?", order.ID).Count(&violations)
	if violations != 1 {
		t.Fatalf("violations = %d, want 1", violations)
	}
	count, err = m.expireDueOrders(order.ExpiresAt.Add(time.Hour))
	if err != nil || count != 0 {
		t.Fatalf("repeat sweep: count=%d err=%v", count, err)
	}
}

func TestPickupOrderAtDeadlineExpires(t *testing.T) {
	m := newTestModule(t)
	readyAt := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	order := createReadyOrder(t, m, "g1", "u1", readyAt)
	result, err := m.pickupOrder(order.ID, "u1", order.ExpiresAt)
	if err != nil || !result.expired || result.picked {
		t.Fatalf("result = %+v, err=%v", result, err)
	}
}

func TestPickupOrderIsIdempotent(t *testing.T) {
	m := newTestModule(t)
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	order := createReadyOrder(t, m, "g1", "u1", now)
	first, err := m.pickupOrder(order.ID, "u1", now.Add(time.Minute))
	if err != nil || !first.picked {
		t.Fatalf("first pickup = %+v, err=%v", first, err)
	}
	second, err := m.pickupOrder(order.ID, "u1", now.Add(2*time.Minute))
	if err != nil || second.picked || second.expired {
		t.Fatalf("second pickup = %+v, err=%v", second, err)
	}
}

func TestPickupViolationsEscalateGlobally(t *testing.T) {
	m := newTestModule(t)
	start := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	for id := uint(1); id <= 3; id++ {
		recordViolation(t, m, id, "u1", start.Add(time.Duration(id)*time.Hour))
	}
	stageOne := loadRestriction(t, m, "u1")
	if stageOne.Stage != 1 || !stageOne.ProbationUntil.Equal(stageOne.BlockedUntil.Add(7*24*time.Hour)) {
		t.Fatalf("stage one = %+v", stageOne)
	}
	afterFirstBan := stageOne.BlockedUntil.Add(time.Hour)
	recordViolation(t, m, 4, "u1", afterFirstBan)
	recordViolation(t, m, 5, "u1", afterFirstBan.Add(time.Hour))
	stageTwo := loadRestriction(t, m, "u1")
	if stageTwo.Stage != 2 || !stageTwo.BlockedUntil.Equal(afterFirstBan.Add(time.Hour+7*24*time.Hour)) || !stageTwo.ProbationUntil.Equal(stageTwo.BlockedUntil.Add(14*24*time.Hour)) {
		t.Fatalf("stage two = %+v", stageTwo)
	}
	afterSecondBan := stageTwo.BlockedUntil.Add(time.Hour)
	recordViolation(t, m, 6, "u1", afterSecondBan)
	stageThree := loadRestriction(t, m, "u1")
	if stageThree.Stage != 3 || !stageThree.BlockedUntil.Equal(afterSecondBan.Add(30*24*time.Hour)) || !stageThree.ProbationUntil.Equal(stageThree.BlockedUntil.Add(28*24*time.Hour)) {
		t.Fatalf("stage three = %+v", stageThree)
	}
}

func TestFirstViolationAfterCompletedProbationStartsNewCycle(t *testing.T) {
	m := newTestModule(t)
	start := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	for id := uint(1); id <= 3; id++ {
		recordViolation(t, m, id, "u1", start.Add(time.Duration(id)*time.Hour))
	}
	state := loadRestriction(t, m, "u1")
	recordViolation(t, m, 4, "u1", state.ProbationUntil)
	recordViolation(t, m, 5, "u1", state.ProbationUntil.Add(time.Hour))
	recordViolation(t, m, 6, "u1", state.ProbationUntil.Add(2*time.Hour))
	resetStage := loadRestriction(t, m, "u1")
	if resetStage.Stage != 1 {
		t.Fatalf("stage after reset = %d, want 1", resetStage.Stage)
	}
}

func TestCompletedProbationResetsRestrictionAndViolations(t *testing.T) {
	m := newTestModule(t)
	start := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	for id := uint(1); id <= 3; id++ {
		recordViolation(t, m, id, "u1", start.Add(time.Duration(id)*time.Hour))
	}
	state := loadRestriction(t, m, "u1")
	if _, err := m.restrictionForUser("u1", state.ProbationUntil); err != nil {
		t.Fatalf("restrictionForUser: %v", err)
	}
	var restrictions, violations int64
	m.getDB().Model(&BrewRestriction{}).Where("user_id = ?", "u1").Count(&restrictions)
	m.getDB().Model(&PickupViolation{}).Where("user_id = ?", "u1").Count(&violations)
	if restrictions != 0 || violations != 0 {
		t.Fatalf("after reset: restrictions=%d violations=%d", restrictions, violations)
	}
}

func TestOldViolationsDoNotTriggerFirstBan(t *testing.T) {
	m := newTestModule(t)
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	recordViolation(t, m, 1, "u1", now.Add(-violationWindow-time.Hour))
	recordViolation(t, m, 2, "u1", now.Add(-time.Hour))
	recordViolation(t, m, 3, "u1", now)
	var count int64
	m.getDB().Model(&BrewRestriction{}).Where("user_id = ?", "u1").Count(&count)
	if count != 0 {
		t.Fatal("a violation older than 90 days must not trigger a ban")
	}
}

func TestRestrictedBrewIsRejectedEphemerallyAcrossGuilds(t *testing.T) {
	m := newTestModule(t)
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	useNow(m, t, now)
	if err := m.getDB().Create(&BrewRestriction{UserID: "u1", Stage: 1, CycleStartedAt: now.Add(-time.Hour), BlockedUntil: now.Add(24 * time.Hour), ProbationUntil: now.Add(8 * 24 * time.Hour)}).Error; err != nil {
		t.Fatalf("create restriction: %v", err)
	}
	responses, edits, sleeps := captureBrewIO(m)
	m.handleBrewInteraction(nil, makeBrewInteraction("another-guild", strOpt("drink", "coffee")))
	if len(*responses) != 1 || !(*responses)[0].ephemeral || !strings.Contains((*responses)[0].content, "cannot use `/brew`") {
		t.Fatalf("responses = %+v", *responses)
	}
	if len(*edits) != 0 || len(*sleeps) != 0 {
		t.Fatalf("blocked brew edited=%d slept=%d", len(*edits), len(*sleeps))
	}
}

func TestRunOrderExpiryProcessesExistingOrderOnStartup(t *testing.T) {
	m := newTestModule(t)
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	order := createReadyOrder(t, m, "g1", "u1", now.Add(-pickupWindow-time.Minute))
	ctx, cancel := context.WithCancel(context.Background())
	previousNow := m.nowFunc
	m.nowFunc = func() time.Time {
		cancel()
		return now
	}
	t.Cleanup(func() { m.nowFunc = previousNow })
	m.runOrderExpiry(ctx)
	if err := m.getDB().First(&order, order.ID).Error; err != nil {
		t.Fatalf("reload order: %v", err)
	}
	if order.Status != orderStatusExpired {
		t.Fatalf("status = %q, want expired", order.Status)
	}
}

func TestOnlyOneOpenOrderPerUserAndGuild(t *testing.T) {
	m := newTestModule(t)
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	useNow(m, t, now)
	first, err := m.dispense("g1", "u1", "coffee", false, false)
	if err != nil || !first.ok {
		t.Fatalf("first dispense: out=%+v err=%v", first, err)
	}
	second, err := m.dispense("g1", "u1", "espresso", false, false)
	if err != nil || second.ok || !strings.Contains(second.failMsg, "already have") {
		t.Fatalf("second dispense: out=%+v err=%v", second, err)
	}
	otherGuild, err := m.dispense("g2", "u1", "espresso", false, false)
	if err != nil || !otherGuild.ok {
		t.Fatalf("other guild dispense: out=%+v err=%v", otherGuild, err)
	}
}

func TestBrewSurvivesExpirySweep(t *testing.T) {
	m := newTestModule(t)
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	m.nowFunc = func() time.Time { return now }
	_, edits, _ := captureBrewIO(m)
	var components []discordgo.MessageComponent
	m.editWithComponents = func(_ *discordgo.Session, _ *discordgo.InteractionCreate, content string, comps []discordgo.MessageComponent) {
		*edits = append(*edits, content)
		components = comps
	}
	m.sleep = func(wait time.Duration) {
		now = now.Add(wait)
		if count, err := m.expireDueOrders(now); err != nil || count != 0 {
			t.Fatalf("sweep at estimated ready time: count=%d err=%v", count, err)
		}
		// Completion can lag behind the estimate because of LLM/Discord latency.
		now = now.Add(time.Minute)
		if count, err := m.expireDueOrders(now); err != nil || count != 0 {
			t.Fatalf("sweep after estimated ready time: count=%d err=%v", count, err)
		}
	}
	m.handleBrewInteraction(nil, makeBrewInteraction("g1", strOpt("drink", "tea_rooibos")))
	if len(*edits) != 2 || !strings.Contains((*edits)[1], "Rooibos tea") || len(components) != 1 {
		t.Fatalf("expected ready message with pickup button: edits=%v components=%v", *edits, components)
	}
	var order DrinkOrder
	if err := m.getDB().First(&order).Error; err != nil {
		t.Fatalf("load order: %v", err)
	}
	if order.Status != orderStatusReady || !order.ReadyAt.Equal(now) || !order.ExpiresAt.Equal(now.Add(pickupWindow)) {
		t.Fatalf("expected full pickup window from actual completion: %+v", order)
	}
	result, err := m.pickupOrder(order.ID, "u1", now.Add(pickupWindow-time.Second))
	if err != nil || !result.picked {
		t.Fatalf("pickup = %+v, err=%v", result, err)
	}
}

func TestOpenStoreReleasesInterruptedBrewWithoutViolation(t *testing.T) {
	for _, offset := range []time.Duration{-time.Minute, time.Minute} {
		t.Run(offset.String(), func(t *testing.T) {
			m := New()
			now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
			useNow(m, t, now)
			path := filepath.Join(t.TempDir(), "coffee.db")
			if err := m.openStore(path); err != nil {
				t.Fatalf("open store: %v", err)
			}
			t.Cleanup(func() {
				if err := m.closeStore(); err != nil {
					t.Errorf("close store: %v", err)
				}
			})
			order := DrinkOrder{GuildID: "g1", UserID: "u1", Drink: "tea_rooibos", Status: orderStatusBrewing, ReadyAt: now.Add(offset)}
			if err := m.getDB().Create(&order).Error; err != nil {
				t.Fatalf("create order: %v", err)
			}
			ready := createReadyOrder(t, m, "g1", "u2", now)
			if err := m.closeStore(); err != nil {
				t.Fatalf("close store before restart: %v", err)
			}
			if err := m.openStore(path); err != nil {
				t.Fatalf("reopen store: %v", err)
			}
			if err := m.getDB().First(&order, order.ID).Error; err != nil {
				t.Fatalf("reload order: %v", err)
			}
			var violations int64
			if err := m.getDB().Model(&PickupViolation{}).Count(&violations).Error; err != nil {
				t.Fatalf("count violations: %v", err)
			}
			if order.Status != orderStatusExpired || order.ExpiredAt == nil || !order.ExpiredAt.Equal(now) || violations != 0 {
				t.Fatalf("order=%+v violations=%d", order, violations)
			}
			result, err := m.pickupOrder(ready.ID, "u2", now)
			if err != nil || !result.picked {
				t.Fatalf("ready order pickup after restart = %+v, err=%v", result, err)
			}
			out, err := m.dispense("g1", "u1", "tea_rooibos", false, false)
			if err != nil || !out.ok {
				t.Fatalf("brew after restart: out=%+v err=%v", out, err)
			}
		})
	}
}

func TestBackgroundTaskIsRegistered(t *testing.T) {
	tasks := New().Background()
	if len(tasks) != 1 || tasks[0].Name != "coffee-order-expiry" || tasks[0].Run == nil {
		t.Fatalf("background tasks = %+v", tasks)
	}
}

func TestPickupPenaltyStatsShowsStrikesTimeoutsAndProbation(t *testing.T) {
	m := newTestModule(t)
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	for id := uint(1); id <= 3; id++ {
		recordViolation(t, m, id, "blocked", now.Add(-time.Duration(4-id)*time.Hour))
	}
	for id := uint(4); id <= 6; id++ {
		recordViolation(t, m, id, "probation", now.Add(-5*24*time.Hour+time.Duration(id-4)*time.Hour))
	}
	recordViolation(t, m, 7, "old", now.Add(-violationWindow-time.Hour))

	stats, err := m.pickupPenaltyStats(now)
	if err != nil {
		t.Fatalf("pickupPenaltyStats: %v", err)
	}
	if len(stats) != 2 {
		t.Fatalf("stats = %+v, want blocked and probation users", stats)
	}
	byUser := make(map[string]pickupPenaltyStat, len(stats))
	for _, stat := range stats {
		byUser[stat.UserID] = stat
	}
	if got := byUser["blocked"]; got.Strikes != 3 || got.Stage != 1 || !now.Before(got.BlockedUntil) {
		t.Fatalf("blocked stat = %+v", got)
	}
	if got := byUser["probation"]; got.Strikes != 3 || got.Stage != 1 || now.Before(got.BlockedUntil) || !now.Before(got.ProbationUntil) {
		t.Fatalf("probation stat = %+v", got)
	}
}
