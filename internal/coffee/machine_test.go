package coffee

import (
	"strings"
	"testing"
)

// setLevels mutates the guild's inventory directly, for arranging test states.
func setLevels(m *Module, t *testing.T, guildID string, mut func(*MachineInventory)) {
	t.Helper()
	inv, err := m.getOrSeedInventory(guildID)
	if err != nil {
		t.Fatalf("seed inventory: %v", err)
	}
	mut(&inv)
	if err := m.getDB().Save(&inv).Error; err != nil {
		t.Fatalf("save inventory: %v", err)
	}
}

func countDrinks(m *Module, t *testing.T, guildID string) int64 {
	t.Helper()
	var c int64
	if err := m.getDB().Model(&DrinkEvent{}).Where("guild_id = ?", guildID).Count(&c).Error; err != nil {
		t.Fatalf("count drinks: %v", err)
	}
	return c
}

func countRefills(m *Module, t *testing.T, guildID, part string) int64 {
	t.Helper()
	var c int64
	if err := m.getDB().Model(&RefillEvent{}).Where("guild_id = ? AND part = ?", guildID, part).Count(&c).Error; err != nil {
		t.Fatalf("count refills: %v", err)
	}
	return c
}

func TestMenuDefaultIsCoffee(t *testing.T) {
	if menu[0].key != "coffee" {
		t.Errorf("default drink = %q, want coffee", menu[0].key)
	}
	if _, ok := recipeByKey("latte_macchiato"); !ok {
		t.Error("expected latte_macchiato in menu")
	}
	if _, ok := recipeByKey("nope"); ok {
		t.Error("expected unknown key to be absent")
	}
}

func TestGetOrSeedInventory_SeedsFullMachine(t *testing.T) {
	m := newTestModule(t)
	inv, err := m.getOrSeedInventory("g1")
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if inv.BeansMildGrams != maxBeansMildG || inv.BeansEspressoGrams != maxBeansEspressoG ||
		inv.WaterMl != maxWaterMl || inv.MilkMl != maxMilkMl || inv.GroundsGrams != 0 {
		t.Errorf("seeded inventory not full/empty-grounds: %+v", inv)
	}
}

func TestDispenseCoffee_DeductsAndRecords(t *testing.T) {
	m := newTestModule(t)
	out, err := m.dispense("g1", "user1", "coffee", false, false)
	if err != nil {
		t.Fatalf("dispense: %v", err)
	}
	if !out.ok {
		t.Fatalf("expected ok, got failMsg=%q", out.failMsg)
	}
	inv := out.inventory
	if inv.BeansMildGrams != maxBeansMildG-11 {
		t.Errorf("mild beans = %d, want %d", inv.BeansMildGrams, maxBeansMildG-11)
	}
	if inv.WaterMl != maxWaterMl-120 {
		t.Errorf("water = %d, want %d", inv.WaterMl, maxWaterMl-120)
	}
	if inv.GroundsGrams != 20 {
		t.Errorf("grounds = %d, want 20", inv.GroundsGrams)
	}
	if inv.BeansEspressoGrams != maxBeansEspressoG || inv.MilkMl != maxMilkMl {
		t.Errorf("espresso/milk should be untouched: %+v", inv)
	}
	if c := countDrinks(m, t, "g1"); c != 1 {
		t.Errorf("drink events = %d, want 1", c)
	}
}

func TestDispenseEspresso_UsesEspressoHopper(t *testing.T) {
	m := newTestModule(t)
	out, err := m.dispense("g1", "u", "espresso", false, false)
	if err != nil || !out.ok {
		t.Fatalf("dispense espresso failed: err=%v fail=%q", err, out.failMsg)
	}
	if out.inventory.BeansEspressoGrams != maxBeansEspressoG-9 {
		t.Errorf("espresso beans = %d, want %d", out.inventory.BeansEspressoGrams, maxBeansEspressoG-9)
	}
	if out.inventory.BeansMildGrams != maxBeansMildG {
		t.Errorf("mild beans should be untouched, got %d", out.inventory.BeansMildGrams)
	}
}

func TestDispenseMilkSplash_OnBlackDrink(t *testing.T) {
	m := newTestModule(t)
	out, err := m.dispense("g1", "u", "coffee", true, false)
	if err != nil || !out.ok {
		t.Fatalf("dispense failed: err=%v fail=%q", err, out.failMsg)
	}
	if !out.splashMilk {
		t.Error("expected splashMilk=true for coffee with milk")
	}
	if out.inventory.MilkMl != maxMilkMl-addMilkMl {
		t.Errorf("milk = %d, want %d", out.inventory.MilkMl, maxMilkMl-addMilkMl)
	}
	var de DrinkEvent
	m.getDB().Where("guild_id = ?", "g1").First(&de)
	if !de.WithMilk {
		t.Error("DrinkEvent.WithMilk should be true")
	}
}

func TestDispenseMilkCoffee_IgnoresMilkArg(t *testing.T) {
	m := newTestModule(t)
	out, err := m.dispense("g1", "u", "milk_coffee", true, false)
	if err != nil || !out.ok {
		t.Fatalf("dispense failed: err=%v fail=%q", err, out.failMsg)
	}
	if out.splashMilk {
		t.Error("milk_coffee does not allow an extra splash; splashMilk should be false")
	}
	// only the intrinsic 120 ml, no extra 40 ml splash
	if out.inventory.MilkMl != maxMilkMl-120 {
		t.Errorf("milk = %d, want %d", out.inventory.MilkMl, maxMilkMl-120)
	}
}

func TestDispenseSugar_IsCosmetic(t *testing.T) {
	m := newTestModule(t)
	out, err := m.dispense("g1", "u", "coffee", false, true)
	if err != nil || !out.ok {
		t.Fatalf("dispense failed: err=%v fail=%q", err, out.failMsg)
	}
	// sugar consumes nothing beyond the plain coffee recipe
	if out.inventory.BeansMildGrams != maxBeansMildG-11 || out.inventory.MilkMl != maxMilkMl {
		t.Errorf("sugar should not consume inventory: %+v", out.inventory)
	}
	var de DrinkEvent
	m.getDB().Where("guild_id = ?", "g1").First(&de)
	if !de.WithSugar || de.WithMilk {
		t.Errorf("DrinkEvent flags wrong: milk=%v sugar=%v", de.WithMilk, de.WithSugar)
	}
}

func TestDispenseHotWater_NoBeansNoGrounds(t *testing.T) {
	m := newTestModule(t)
	out, err := m.dispense("g1", "u", "hot_water", false, false)
	if err != nil || !out.ok {
		t.Fatalf("dispense failed: err=%v fail=%q", err, out.failMsg)
	}
	if out.inventory.WaterMl != maxWaterMl-200 {
		t.Errorf("water = %d, want %d", out.inventory.WaterMl, maxWaterMl-200)
	}
	if out.inventory.GroundsGrams != 0 {
		t.Errorf("hot water should produce no grounds, got %d", out.inventory.GroundsGrams)
	}
	if out.inventory.BeansMildGrams != maxBeansMildG || out.inventory.BeansEspressoGrams != maxBeansEspressoG {
		t.Error("hot water should use no beans")
	}
}

func TestDispenseBlockedOnLowWater(t *testing.T) {
	m := newTestModule(t)
	setLevels(m, t, "g1", func(inv *MachineInventory) { inv.WaterMl = 50 })

	out, err := m.dispense("g1", "u", "coffee", false, false)
	if err != nil {
		t.Fatalf("dispense: %v", err)
	}
	if out.ok {
		t.Fatal("expected dispense to be blocked on low water")
	}
	if !strings.Contains(out.failMsg, "water") {
		t.Errorf("failMsg = %q, want it to mention water", out.failMsg)
	}
}

func TestDispenseBlockedOnLowBeans(t *testing.T) {
	m := newTestModule(t)
	setLevels(m, t, "g1", func(inv *MachineInventory) { inv.BeansMildGrams = 5 })

	out, _ := m.dispense("g1", "u", "coffee", false, false)
	if out.ok {
		t.Fatal("expected block on low mild beans")
	}
	if !strings.Contains(out.failMsg, "mild beans") {
		t.Errorf("failMsg = %q, want mild beans", out.failMsg)
	}
}

func TestDispenseBlockedOnLowMilk(t *testing.T) {
	m := newTestModule(t)
	setLevels(m, t, "g1", func(inv *MachineInventory) { inv.MilkMl = 100 })

	out, _ := m.dispense("g1", "u", "milk_coffee", false, false) // needs 120 ml
	if out.ok {
		t.Fatal("expected block on low milk")
	}
	if !strings.Contains(out.failMsg, "milk") {
		t.Errorf("failMsg = %q, want milk", out.failMsg)
	}
}

func TestDispenseBlockedOnFullGrounds(t *testing.T) {
	m := newTestModule(t)
	setLevels(m, t, "g1", func(inv *MachineInventory) { inv.GroundsGrams = maxGroundsG - 5 }) // coffee adds 20

	out, _ := m.dispense("g1", "u", "coffee", false, false)
	if out.ok {
		t.Fatal("expected block on full grounds container")
	}
	if !strings.Contains(out.failMsg, "grounds") {
		t.Errorf("failMsg = %q, want grounds", out.failMsg)
	}
}

func TestDispenseDoesNotMutateOnFailure(t *testing.T) {
	m := newTestModule(t)
	setLevels(m, t, "g1", func(inv *MachineInventory) { inv.WaterMl = 10 })

	_, _ = m.dispense("g1", "u", "coffee", false, false)

	inv, _ := m.getOrSeedInventory("g1")
	if inv.WaterMl != 10 || inv.BeansMildGrams != maxBeansMildG || inv.GroundsGrams != 0 {
		t.Errorf("inventory mutated on failed dispense: %+v", inv)
	}
	if c := countDrinks(m, t, "g1"); c != 0 {
		t.Errorf("failed dispense should record no drink, got %d", c)
	}
}

func TestDispenseUnknownDrink(t *testing.T) {
	m := newTestModule(t)
	out, err := m.dispense("g1", "u", "frappuccino", false, false)
	if err != nil {
		t.Fatalf("dispense: %v", err)
	}
	if out.ok || !strings.Contains(out.failMsg, "Unknown drink") {
		t.Errorf("expected unknown-drink failure, got ok=%v msg=%q", out.ok, out.failMsg)
	}
}

func TestRefill_TopsToMaxAndRecords(t *testing.T) {
	m := newTestModule(t)
	setLevels(m, t, "g1", func(inv *MachineInventory) { inv.WaterMl = 500 })

	out, err := m.refill("g1", "user1", "water")
	if err != nil {
		t.Fatalf("refill: %v", err)
	}
	if out.alreadyFull {
		t.Fatal("did not expect alreadyFull")
	}
	if out.added != maxWaterMl-500 {
		t.Errorf("added = %d, want %d", out.added, maxWaterMl-500)
	}
	if out.inventory.WaterMl != maxWaterMl {
		t.Errorf("water = %d, want full %d", out.inventory.WaterMl, maxWaterMl)
	}
	var ev RefillEvent
	if err := m.getDB().Where("guild_id = ? AND part = ?", "g1", "water").First(&ev).Error; err != nil {
		t.Fatalf("expected a refill event: %v", err)
	}
	if ev.UserID != "user1" || ev.Amount != maxWaterMl-500 {
		t.Errorf("refill event = %+v", ev)
	}
}

func TestRefill_AlreadyFull(t *testing.T) {
	m := newTestModule(t)
	out, err := m.refill("g1", "user1", "milk") // fresh machine, milk already full
	if err != nil {
		t.Fatalf("refill: %v", err)
	}
	if !out.alreadyFull {
		t.Error("expected alreadyFull on a full tank")
	}
	if c := countRefills(m, t, "g1", "milk"); c != 0 {
		t.Errorf("full-tank refill should record nothing, got %d", c)
	}
}

func TestEmptyGrounds(t *testing.T) {
	m := newTestModule(t)
	setLevels(m, t, "g1", func(inv *MachineInventory) { inv.GroundsGrams = 240 })

	out, err := m.emptyGrounds("g1", "user1")
	if err != nil {
		t.Fatalf("empty: %v", err)
	}
	if out.alreadyEmpty {
		t.Fatal("did not expect alreadyEmpty")
	}
	if out.removed != 240 || out.inventory.GroundsGrams != 0 {
		t.Errorf("removed=%d grounds=%d, want 240/0", out.removed, out.inventory.GroundsGrams)
	}
	if c := countRefills(m, t, "g1", partGrounds); c != 1 {
		t.Errorf("expected 1 grounds-empty event, got %d", c)
	}
}

func TestEmptyGrounds_AlreadyEmpty(t *testing.T) {
	m := newTestModule(t)
	out, err := m.emptyGrounds("g1", "user1")
	if err != nil {
		t.Fatalf("empty: %v", err)
	}
	if !out.alreadyEmpty {
		t.Error("expected alreadyEmpty on a fresh machine")
	}
	if c := countRefills(m, t, "g1", partGrounds); c != 0 {
		t.Errorf("empty-on-empty should record nothing, got %d", c)
	}
}

func TestPerGuildIsolation(t *testing.T) {
	m := newTestModule(t)
	if _, err := m.dispense("g1", "u", "coffee", false, false); err != nil {
		t.Fatalf("dispense g1: %v", err)
	}
	g1, _ := m.getOrSeedInventory("g1")
	g2, _ := m.getOrSeedInventory("g2")
	if g1.BeansMildGrams != maxBeansMildG-11 {
		t.Errorf("g1 mild beans = %d, want depleted", g1.BeansMildGrams)
	}
	if g2.BeansMildGrams != maxBeansMildG || g2.GroundsGrams != 0 {
		t.Errorf("g2 should be untouched/full, got %+v", g2)
	}
}

func TestLeaderboards(t *testing.T) {
	m := newTestModule(t)
	d := m.getDB()
	// drinks: A x3, B x1
	for range 3 {
		d.Create(&DrinkEvent{GuildID: "lg", UserID: "A", Drink: "coffee"})
	}
	d.Create(&DrinkEvent{GuildID: "lg", UserID: "B", Drink: "espresso"})
	// refills: B x2, A x1 ; plus a grounds empty by A (excluded from refillers)
	d.Create(&RefillEvent{GuildID: "lg", UserID: "B", Part: "milk", Amount: 100})
	d.Create(&RefillEvent{GuildID: "lg", UserID: "B", Part: "water", Amount: 100})
	d.Create(&RefillEvent{GuildID: "lg", UserID: "A", Part: "water", Amount: 100})
	d.Create(&RefillEvent{GuildID: "lg", UserID: "A", Part: partGrounds, Amount: 50})
	// a different guild must not leak in
	d.Create(&DrinkEvent{GuildID: "other", UserID: "Z", Drink: "coffee"})

	drinkers, err := m.topDrinkers("lg", 3)
	if err != nil {
		t.Fatalf("topDrinkers: %v", err)
	}
	if len(drinkers) != 2 || drinkers[0].UserID != "A" || drinkers[0].Count != 3 || drinkers[1].UserID != "B" {
		t.Errorf("topDrinkers = %+v", drinkers)
	}

	refillers, err := m.topRefillers("lg", 3)
	if err != nil {
		t.Fatalf("topRefillers: %v", err)
	}
	if len(refillers) != 2 || refillers[0].UserID != "B" || refillers[0].Count != 2 {
		t.Errorf("topRefillers = %+v (grounds must be excluded)", refillers)
	}

	if c, _ := m.groundsEmptiedCount("lg"); c != 1 {
		t.Errorf("groundsEmptiedCount = %d, want 1", c)
	}
}

func TestFormatDispenseSuccess(t *testing.T) {
	r, _ := recipeByKey("coffee")
	inv := MachineInventory{BeansMildGrams: 989, BeansEspressoGrams: 1000, WaterMl: 1880, MilkMl: 960, GroundsGrams: 20}
	got := formatDispenseSuccess(r, true, true, inv)
	if !strings.Contains(got, "Coffee with milk and sugar") {
		t.Errorf("missing extras phrasing: %q", got)
	}
	if !strings.Contains(got, "Grounds 20/500g") {
		t.Errorf("missing grounds level: %q", got)
	}

	plain := formatDispenseSuccess(r, false, false, inv)
	if strings.Contains(plain, "with") {
		t.Errorf("plain drink should have no extras phrasing: %q", plain)
	}
}

func TestFormatStatus(t *testing.T) {
	inv := MachineInventory{BeansMildGrams: 500, BeansEspressoGrams: 1000, WaterMl: 1000, MilkMl: 1000, GroundsGrams: 250}
	got := formatStatus(inv,
		[]userCount{{UserID: "A", Count: 3}},
		nil,
		2)
	if !strings.Contains(got, "Mild beans: 500/1000g (50%)") {
		t.Errorf("missing mild beans line with percent: %q", got)
	}
	if !strings.Contains(got, "<@A>: 3 drinks") {
		t.Errorf("missing barista leaderboard: %q", got)
	}
	if !strings.Contains(got, "Top refillers") || !strings.Contains(got, "_none yet_") {
		t.Errorf("missing empty refillers section: %q", got)
	}
	if !strings.Contains(got, "Grounds emptied 2 times") {
		t.Errorf("missing grounds-emptied tally: %q", got)
	}
}

func TestPercent(t *testing.T) {
	cases := []struct {
		cur, max, want int
	}{
		{0, 1000, 0},
		{500, 1000, 50},
		{1000, 1000, 100},
		{989, 1000, 99},
		{5, 0, 0},
	}
	for _, c := range cases {
		if got := percent(c.cur, c.max); got != c.want {
			t.Errorf("percent(%d,%d) = %d, want %d", c.cur, c.max, got, c.want)
		}
	}
}
