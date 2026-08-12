package coffee

import (
	"context"
	"strings"
	"testing"
	"time"

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
	useNow(m, t, now)
	order := createReadyOrder(t, m, "g1", "u1", now.Add(-pickupWindow-time.Minute))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		m.runOrderExpiry(ctx)
	}()
	deadline := time.After(time.Second)
	for {
		if err := m.getDB().First(&order, order.ID).Error; err != nil {
			t.Fatalf("reload order: %v", err)
		}
		if order.Status == orderStatusExpired {
			break
		}
		select {
		case <-deadline:
			t.Fatal("startup sweep did not expire order")
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	<-done
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

func TestStartupSweepReleasesInterruptedBrewWithoutViolation(t *testing.T) {
	m := newTestModule(t)
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	order := DrinkOrder{GuildID: "g1", UserID: "u1", Drink: "coffee", Status: orderStatusBrewing, ReadyAt: now.Add(-time.Minute)}
	if err := m.getDB().Create(&order).Error; err != nil {
		t.Fatalf("create order: %v", err)
	}
	if _, err := m.expireDueOrders(now); err != nil {
		t.Fatalf("expireDueOrders: %v", err)
	}
	if err := m.getDB().First(&order, order.ID).Error; err != nil {
		t.Fatalf("reload order: %v", err)
	}
	var violations int64
	m.getDB().Model(&PickupViolation{}).Where("order_id = ?", order.ID).Count(&violations)
	if order.Status != orderStatusExpired || violations != 0 {
		t.Fatalf("order=%+v violations=%d", order, violations)
	}
}

func TestBackgroundTaskIsRegistered(t *testing.T) {
	tasks := New().Background()
	if len(tasks) != 1 || tasks[0].Name != "coffee-order-expiry" || tasks[0].Run == nil {
		t.Fatalf("background tasks = %+v", tasks)
	}
}
