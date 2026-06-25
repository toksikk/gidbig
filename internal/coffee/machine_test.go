package coffee

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
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
	got := formatDispenseSuccess(r, true, true, "")
	if !strings.Contains(got, "Coffee with milk and sugar") {
		t.Errorf("missing extras phrasing: %q", got)
	}
	// the per-brew message must NOT carry machine stats (those live in status)
	if strings.Contains(got, "Grounds") || strings.Contains(got, "/500g") || strings.Contains(got, "·") {
		t.Errorf("brew message should not include machine stats: %q", got)
	}

	plain := formatDispenseSuccess(r, false, false, "")
	if strings.Contains(plain, "with") {
		t.Errorf("plain drink should have no extras phrasing: %q", plain)
	}
}

func TestFormatDispenseSuccess_Tea(t *testing.T) {
	hot, _ := recipeByKey("hot_water")

	tea := formatDispenseSuccess(hot, false, false, "peppermint")
	if !strings.Contains(tea, "🍵 Here's your Peppermint tea!") {
		t.Errorf("tea phrasing wrong: %q", tea)
	}

	teaMilk := formatDispenseSuccess(hot, true, false, "earl_grey")
	if !strings.Contains(teaMilk, "Earl Grey tea with milk") {
		t.Errorf("tea+milk phrasing wrong: %q", teaMilk)
	}

	plainWater := formatDispenseSuccess(hot, false, false, "")
	if !strings.Contains(plainWater, "☕ Here's your Hot water!") {
		t.Errorf("plain hot water phrasing wrong: %q", plainWater)
	}

	// tea is ignored for non-hot-water drinks
	coffee, _ := recipeByKey("coffee")
	c := formatDispenseSuccess(coffee, false, false, "green")
	if strings.Contains(c, "tea") || strings.Contains(c, "🍵") {
		t.Errorf("tea should be ignored for coffee: %q", c)
	}
}

func TestBrewTimeVariesByDrink(t *testing.T) {
	hot, _ := recipeByKey("hot_water")
	flat, _ := recipeByKey("flat_white")
	if brewTime(hot) >= brewTime(flat) {
		t.Errorf("hot water (%v) should brew faster than flat white (%v)", brewTime(hot), brewTime(flat))
	}
	if brewTime(hot) <= 0 {
		t.Errorf("brew time must be positive, got %v", brewTime(hot))
	}
}

type respCall struct {
	content   string
	ephemeral bool
}

// captureBrewIO stubs the interaction response, edit, and sleep hooks so brew
// handler behavior can be asserted without a live Discord session. respondUpdate
// (used by the interactive-menu brew path) is captured alongside respond.
func captureBrewIO(m *Module) (*[]respCall, *[]string, *[]time.Duration) {
	resp := &[]respCall{}
	edits := &[]string{}
	sleeps := &[]time.Duration{}
	m.respond = func(_ *discordgo.Session, _ *discordgo.InteractionCreate, content string, ephemeral bool) {
		*resp = append(*resp, respCall{content, ephemeral})
	}
	m.respondUpdate = func(_ *discordgo.Session, _ *discordgo.InteractionCreate, content string) {
		*resp = append(*resp, respCall{content, false})
	}
	m.editDeferredResponse = func(_ *discordgo.Session, _ *discordgo.InteractionCreate, content string) {
		*edits = append(*edits, content)
	}
	m.editWithComponents = func(_ *discordgo.Session, _ *discordgo.InteractionCreate, content string, _ []discordgo.MessageComponent) {
		*edits = append(*edits, content)
	}
	m.sleep = func(d time.Duration) { *sleeps = append(*sleeps, d) }
	return resp, edits, sleeps
}

type menuCall struct {
	content string
	comps   []discordgo.MessageComponent
}

// captureMenuIO stubs the openMenu/updateMenu hooks for interactive-menu tests.
func captureMenuIO(m *Module) (opens, updates *[]menuCall) {
	opens = &[]menuCall{}
	updates = &[]menuCall{}
	m.openMenu = func(_ *discordgo.Session, _ *discordgo.InteractionCreate, content string, comps []discordgo.MessageComponent) {
		*opens = append(*opens, menuCall{content, comps})
	}
	m.updateMenu = func(_ *discordgo.Session, _ *discordgo.InteractionCreate, content string, comps []discordgo.MessageComponent) {
		*updates = append(*updates, menuCall{content, comps})
	}
	return opens, updates
}

func makeBrewComponent(guildID, customID string, values ...string) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			GuildID:   guildID,
			ChannelID: "ch1",
			Type:      discordgo.InteractionMessageComponent,
			Member:    &discordgo.Member{User: &discordgo.User{ID: "u1"}},
			Data: discordgo.MessageComponentInteractionData{
				CustomID: customID,
				Values:   values,
			},
		},
	}
}

func makeBrewInteraction(guildID string, opts ...*discordgo.ApplicationCommandInteractionDataOption) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			GuildID:   guildID,
			ChannelID: "ch1",
			Type:      discordgo.InteractionApplicationCommand,
			Member:    &discordgo.Member{User: &discordgo.User{ID: "u1"}},
			Data: discordgo.ApplicationCommandInteractionData{
				Name:    "coffee",
				Options: opts,
			},
		},
	}
}

func strOpt(name, value string) *discordgo.ApplicationCommandInteractionDataOption {
	return &discordgo.ApplicationCommandInteractionDataOption{
		Name:  name,
		Type:  discordgo.ApplicationCommandOptionString,
		Value: value,
	}
}

func boolOpt(name string, value bool) *discordgo.ApplicationCommandInteractionDataOption {
	return &discordgo.ApplicationCommandInteractionDataOption{
		Name:  name,
		Type:  discordgo.ApplicationCommandOptionBoolean,
		Value: value,
	}
}

func TestHandleBrew_BlockedShowsErrorNoBrewing(t *testing.T) {
	m := newTestModule(t)
	stubLLM(m, t, "", nil) // empty reply -> deterministic fallback
	resp, edits, sleeps := captureBrewIO(m)
	setLevels(m, t, "g1", func(inv *MachineInventory) { inv.WaterMl = 10 })

	m.handleCoffeeInteraction(nil, makeBrewInteraction("g1", strOpt("drink", "coffee")))

	if len(*resp) != 1 || !(*resp)[0].ephemeral {
		t.Fatalf("expected 1 ephemeral error response, got %+v", *resp)
	}
	if !strings.Contains((*resp)[0].content, "water") {
		t.Errorf("error should name the missing ingredient: %q", (*resp)[0].content)
	}
	if len(*edits) != 0 || len(*sleeps) != 0 {
		t.Errorf("blocked brew must not brew/edit: edits=%d sleeps=%d", len(*edits), len(*sleeps))
	}
	if c := countDrinks(m, t, "g1"); c != 0 {
		t.Errorf("blocked brew should record no drink, got %d", c)
	}
}

func TestHandleBrew_SuccessShowsBrewingThenFinalNoStats(t *testing.T) {
	m := newTestModule(t)
	now := time.Date(2026, 6, 25, 9, 0, 0, 0, time.UTC)
	useNow(m, t, now)
	stubLLM(m, t, "", nil) // fallback messages
	resp, edits, sleeps := captureBrewIO(m)

	m.handleCoffeeInteraction(nil, makeBrewInteraction("g1", strOpt("drink", "coffee")))

	if len(*resp) != 1 || !(*resp)[0].ephemeral {
		t.Fatalf("expected 1 ephemeral brewing response, got %+v", *resp)
	}
	if !strings.Contains((*resp)[0].content, "Brewing") {
		t.Errorf("first response should be a brewing status: %q", (*resp)[0].content)
	}
	coffee, _ := recipeByKey("coffee")
	wantTS := fmt.Sprintf("<t:%d:R>", now.Add(brewTime(coffee)).Unix())
	if !strings.Contains((*resp)[0].content, wantTS) {
		t.Errorf("brewing status should carry the relative ready time %q, got %q", wantTS, (*resp)[0].content)
	}
	if len(*sleeps) != 1 || (*sleeps)[0] != brewTime(coffee) {
		t.Errorf("expected one sleep of %v, got %v", brewTime(coffee), *sleeps)
	}
	if len(*edits) != 1 {
		t.Fatalf("expected 1 final edit, got %d", len(*edits))
	}
	final := (*edits)[0]
	if !strings.Contains(final, "Here's your Coffee") {
		t.Errorf("final message wrong: %q", final)
	}
	if strings.Contains(final, "Grounds") || strings.Contains(final, "·") {
		t.Errorf("final brew message must not include machine stats: %q", final)
	}
	if c := countDrinks(m, t, "g1"); c != 1 {
		t.Errorf("successful brew should record 1 drink, got %d", c)
	}
}

func TestHandleBrew_UsesLLMForVariation(t *testing.T) {
	m := newTestModule(t)
	stubLLM(m, t, "Dein Kaffee ist fertig!", nil) // non-empty -> LLM text used
	_, edits, _ := captureBrewIO(m)

	m.handleCoffeeInteraction(nil, makeBrewInteraction("g1", strOpt("drink", "coffee")))

	if len(*edits) != 1 || (*edits)[0] != "Dein Kaffee ist fertig!" {
		t.Errorf("final message should come from the LLM, got %v", *edits)
	}
}

func TestTeaLabel(t *testing.T) {
	if l, ok := teaLabel("earl_grey"); !ok || l != "Earl Grey" {
		t.Errorf("teaLabel(earl_grey) = %q,%v", l, ok)
	}
	if _, ok := teaLabel("bubble"); ok {
		t.Error("teaLabel(bubble) should be unknown")
	}
}

func TestFormatStatus(t *testing.T) {
	inv := MachineInventory{BeansMildGrams: 500, BeansEspressoGrams: 1000, WaterMl: 1000, MilkMl: 1000, GroundsGrams: 250}
	got := formatStatus(inv,
		[]userCount{{UserID: "A", Count: 3}},
		nil,
		[]groundsEmptier{{UserID: "A", Count: 2, TotalGrams: 480}},
		[]userCount{{UserID: "B", Count: 1}})
	if !strings.Contains(got, "Mild beans: 500/1000g (50%)") {
		t.Errorf("missing mild beans line with percent: %q", got)
	}
	if !strings.Contains(got, "<@A>: 3 drinks") {
		t.Errorf("missing barista leaderboard: %q", got)
	}
	if !strings.Contains(got, "Top refillers") || !strings.Contains(got, "_none yet_") {
		t.Errorf("missing empty refillers section: %q", got)
	}
	if !strings.Contains(got, "Top grounds-emptiers") {
		t.Errorf("missing grounds-emptiers section: %q", got)
	}
	if !strings.Contains(got, "<@A>: 2× · 480g total · 240g avg") {
		t.Errorf("missing grounds-emptier leaderboard with times/grams/avg: %q", got)
	}
	if !strings.Contains(got, "Slackers") || !strings.Contains(got, "<@B>: 1 misses") {
		t.Errorf("missing slacker section: %q", got)
	}
}

func TestFormatStatus_NoSlackersHidesSection(t *testing.T) {
	inv := MachineInventory{}
	got := formatStatus(inv, nil, nil, nil, nil)
	if strings.Contains(got, "Slackers") {
		t.Errorf("slacker section should be hidden when there are none: %q", got)
	}
	if !strings.Contains(got, "Top grounds-emptiers") || !strings.Contains(got, "_none yet_") {
		t.Errorf("grounds-emptiers section should show even when empty: %q", got)
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

// --- /tea command -----------------------------------------------------------

func makeTeaInteraction(guildID string, opts ...*discordgo.ApplicationCommandInteractionDataOption) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			GuildID:   guildID,
			ChannelID: "ch1",
			Type:      discordgo.InteractionApplicationCommand,
			Member:    &discordgo.Member{User: &discordgo.User{ID: "u1"}},
			Data: discordgo.ApplicationCommandInteractionData{
				Name:    "tea",
				Options: opts,
			},
		},
	}
}

func TestHandleTea_BrewsTeaNotCoffee(t *testing.T) {
	m := newTestModule(t)
	stubLLM(m, t, "", nil) // deterministic fallback
	_, edits, _ := captureBrewIO(m)

	m.handleTeaInteraction(nil, makeTeaInteraction("g1", strOpt("flavor", "rooibos")))

	if len(*edits) != 1 || !strings.Contains((*edits)[0], "Rooibos tea") {
		t.Fatalf("expected a Rooibos tea, got %v", *edits)
	}
	// Tea is hot water: no beans, no grounds, only water consumed.
	inv, _ := m.getOrSeedInventory("g1")
	if inv.GroundsGrams != 0 || inv.BeansMildGrams != maxBeansMildG || inv.BeansEspressoGrams != maxBeansEspressoG {
		t.Errorf("tea should brew from hot water only: %+v", inv)
	}
	if inv.WaterMl != maxWaterMl-200 {
		t.Errorf("tea should consume 200ml water, got %d", inv.WaterMl)
	}
	var de DrinkEvent
	m.getDB().Where("guild_id = ?", "g1").First(&de)
	if de.Drink != "hot_water" {
		t.Errorf("tea DrinkEvent drink = %q, want hot_water", de.Drink)
	}
}

func TestHandleTea_WithMilk(t *testing.T) {
	m := newTestModule(t)
	stubLLM(m, t, "", nil)
	_, edits, _ := captureBrewIO(m)

	m.handleTeaInteraction(nil, makeTeaInteraction("g1", strOpt("flavor", "earl_grey"), boolOpt("milk", true)))

	if len(*edits) != 1 || !strings.Contains((*edits)[0], "Earl Grey tea with milk") {
		t.Fatalf("expected Earl Grey tea with milk, got %v", *edits)
	}
	inv, _ := m.getOrSeedInventory("g1")
	if inv.MilkMl != maxMilkMl-addMilkMl {
		t.Errorf("tea+milk should add a %dml splash, got milk=%d", addMilkMl, inv.MilkMl)
	}
}

// --- Interactive /coffee and /tea menus --------------------------------------

// menuSelectedDrink returns the drink marked Default in the menu's select.
func menuSelectedDrink(t *testing.T, comps []discordgo.MessageComponent) string {
	t.Helper()
	row, ok := comps[0].(discordgo.ActionsRow)
	if !ok {
		t.Fatalf("comps[0] is not an ActionsRow: %T", comps[0])
	}
	sel, ok := row.Components[0].(discordgo.SelectMenu)
	if !ok {
		t.Fatalf("first component is not a SelectMenu: %T", row.Components[0])
	}
	for _, o := range sel.Options {
		if o.Default {
			return o.Value
		}
	}
	return ""
}

// menuCfg parses the full order state out of the Brew button's custom ID.
func menuCfg(t *testing.T, prefix string, comps []discordgo.MessageComponent) brewCfg {
	t.Helper()
	row, ok := comps[1].(discordgo.ActionsRow)
	if !ok {
		t.Fatalf("comps[1] is not an ActionsRow: %T", comps[1])
	}
	btn, ok := row.Components[2].(discordgo.Button)
	if !ok {
		t.Fatalf("third button is not a Button: %T", row.Components[2])
	}
	_, c, ok := parseBrewCfg(prefix, btn.CustomID)
	if !ok {
		t.Fatalf("brew button has unparseable custom ID %q", btn.CustomID)
	}
	return c
}

func TestBrewCfgRoundTrip(t *testing.T) {
	in := brewCfg{choice: "latte_macchiato", milk: true, sugar: false}
	action, out, ok := parseBrewCfg(coffeeCfgPrefix, encodeBrewCfg(coffeeCfgPrefix, "go", in))
	if !ok || action != "go" || out != in {
		t.Fatalf("round trip failed: action=%q out=%+v ok=%v", action, out, ok)
	}
	// The tea menu uses a different prefix, so a coffee custom ID must not parse
	// as tea (prevents the two menus' components from cross-firing).
	if _, _, ok := parseBrewCfg(teaCfgPrefix, encodeBrewCfg(coffeeCfgPrefix, "go", in)); ok {
		t.Error("coffee custom ID should not parse under the tea prefix")
	}
	if _, _, ok := parseBrewCfg(coffeeCfgPrefix, "coffee_brew_cfg:go:coffee"); ok {
		t.Error("truncated custom ID should not parse")
	}
}

func TestCoffeeMenuExcludesHotWater(t *testing.T) {
	for _, r := range coffeeMenu() {
		if r.key == "hot_water" {
			t.Fatal("coffee menu must not offer hot water (that's /tea now)")
		}
	}
	for _, ch := range drinkChoices() {
		if ch.Value == "hot_water" {
			t.Fatal("/coffee drink choices must not include hot water")
		}
	}
}

func TestCoffeeMenuComponents_ReflectState(t *testing.T) {
	comps := coffeeMenuComponents(brewCfg{choice: "espresso", milk: true, sugar: false})
	if d := menuSelectedDrink(t, comps); d != "espresso" {
		t.Errorf("selected drink = %q, want espresso", d)
	}
	if c := menuCfg(t, coffeeCfgPrefix, comps); !c.milk || c.sugar {
		t.Errorf("button state cfg = %+v, want milk on/sugar off", c)
	}
}

func TestCoffee_NoOptionsOpensMenu(t *testing.T) {
	m := newTestModule(t)
	opens, _ := captureMenuIO(m)
	resp, _, _ := captureBrewIO(m)

	m.handleCoffeeInteraction(nil, makeBrewInteraction("g1")) // no options

	if len(*opens) != 1 {
		t.Fatalf("expected the menu to open once, got %d", len(*opens))
	}
	if len(*resp) != 0 {
		t.Errorf("no-options /coffee should not brew directly, got responses %+v", *resp)
	}
	if d := menuSelectedDrink(t, (*opens)[0].comps); d != "coffee" {
		t.Errorf("menu should default to coffee, got %q", d)
	}
	if c := countDrinks(m, t, "g1"); c != 0 {
		t.Errorf("opening the menu should brew nothing, got %d drinks", c)
	}
}

func TestTea_NoOptionsOpensMenu(t *testing.T) {
	m := newTestModule(t)
	opens, _ := captureMenuIO(m)
	resp, _, _ := captureBrewIO(m)

	m.handleTeaInteraction(nil, makeTeaInteraction("g1")) // no options

	if len(*opens) != 1 {
		t.Fatalf("expected the tea menu to open once, got %d", len(*opens))
	}
	if len(*resp) != 0 {
		t.Errorf("no-options /tea should not brew directly, got %+v", *resp)
	}
	if d := menuSelectedDrink(t, (*opens)[0].comps); d != teaFlavors[0].key {
		t.Errorf("tea menu should default to %q, got %q", teaFlavors[0].key, d)
	}
}

func TestCoffeeComponent_TogglesAndSelect(t *testing.T) {
	m := newTestModule(t)
	_, updates := captureMenuIO(m)

	// select espresso
	m.handleCoffeeComponent(nil, makeBrewComponent("g1", encodeBrewCfg(coffeeCfgPrefix, "pick", brewCfg{choice: "coffee"}), "espresso"))
	// toggle milk on (state still carries espresso)
	m.handleCoffeeComponent(nil, makeBrewComponent("g1", encodeBrewCfg(coffeeCfgPrefix, "milk", brewCfg{choice: "espresso"})))
	// toggle sugar on
	m.handleCoffeeComponent(nil, makeBrewComponent("g1", encodeBrewCfg(coffeeCfgPrefix, "sugar", brewCfg{choice: "espresso", milk: true})))

	if len(*updates) != 3 {
		t.Fatalf("expected 3 in-place menu updates, got %d", len(*updates))
	}
	if d := menuSelectedDrink(t, (*updates)[0].comps); d != "espresso" {
		t.Errorf("after select, drink = %q, want espresso", d)
	}
	if c := menuCfg(t, coffeeCfgPrefix, (*updates)[1].comps); !c.milk {
		t.Errorf("after milk toggle, cfg = %+v, want milk on", c)
	}
	if c := menuCfg(t, coffeeCfgPrefix, (*updates)[2].comps); !c.sugar || !c.milk {
		t.Errorf("after sugar toggle, cfg = %+v, want milk+sugar on", c)
	}
}

func TestCoffeeComponent_GoBrews(t *testing.T) {
	m := newTestModule(t)
	stubLLM(m, t, "", nil)
	resp, edits, sleeps := captureBrewIO(m)
	_, _ = captureMenuIO(m)

	m.handleCoffeeComponent(nil, makeBrewComponent("g1", encodeBrewCfg(coffeeCfgPrefix, "go", brewCfg{choice: "espresso", milk: true, sugar: true})))

	if len(*resp) != 1 || !strings.Contains((*resp)[0].content, "Brewing") {
		t.Fatalf("expected an in-place brewing update, got %+v", *resp)
	}
	if len(*sleeps) != 1 {
		t.Errorf("expected one brew delay, got %d", len(*sleeps))
	}
	if len(*edits) != 1 || !strings.Contains((*edits)[0], "Espresso with milk and sugar") {
		t.Fatalf("expected final espresso with milk+sugar, got %v", *edits)
	}
	var de DrinkEvent
	m.getDB().Where("guild_id = ?", "g1").First(&de)
	if de.Drink != "espresso" || !de.WithMilk || !de.WithSugar {
		t.Errorf("brewed DrinkEvent = %+v, want espresso milk+sugar", de)
	}
}

func TestTeaComponent_GoBrews(t *testing.T) {
	m := newTestModule(t)
	stubLLM(m, t, "", nil)
	resp, edits, sleeps := captureBrewIO(m)
	_, _ = captureMenuIO(m)

	m.handleTeaComponent(nil, makeBrewComponent("g1", encodeBrewCfg(teaCfgPrefix, "go", brewCfg{choice: "rooibos", milk: true})))

	if len(*resp) != 1 || !strings.Contains((*resp)[0].content, "Brewing") {
		t.Fatalf("expected an in-place brewing update, got %+v", *resp)
	}
	if len(*sleeps) != 1 {
		t.Errorf("expected one brew delay, got %d", len(*sleeps))
	}
	if len(*edits) != 1 || !strings.Contains((*edits)[0], "Rooibos tea with milk") {
		t.Fatalf("expected final Rooibos tea with milk, got %v", *edits)
	}
	var de DrinkEvent
	m.getDB().Where("guild_id = ?", "g1").First(&de)
	if de.Drink != "hot_water" || !de.WithMilk {
		t.Errorf("tea brew DrinkEvent = %+v, want hot_water with milk", de)
	}
}

func TestTakeCup_Confirms(t *testing.T) {
	m := newTestModule(t)
	resp, _, _ := captureBrewIO(m)

	id := strings.Join([]string{takeCupPrefix, "espresso", ""}, ":")
	m.handleTakeCupComponent(nil, makeBrewComponent("g1", id))

	if len(*resp) != 1 {
		t.Fatalf("expected 1 confirmation update, got %d", len(*resp))
	}
	if !strings.Contains((*resp)[0].content, "grabbed your Espresso") {
		t.Errorf("take-cup confirmation should name the drink: %q", (*resp)[0].content)
	}
}

func TestExecuteBrew_AttachesTakeCupButton(t *testing.T) {
	m := newTestModule(t)
	stubLLM(m, t, "", nil)
	var finalComps []discordgo.MessageComponent
	m.respond = func(_ *discordgo.Session, _ *discordgo.InteractionCreate, _ string, _ bool) {}
	m.editWithComponents = func(_ *discordgo.Session, _ *discordgo.InteractionCreate, _ string, comps []discordgo.MessageComponent) {
		finalComps = comps
	}
	m.sleep = func(time.Duration) {}

	m.handleCoffeeInteraction(nil, makeBrewInteraction("g1", strOpt("drink", "coffee")))

	if len(finalComps) != 1 {
		t.Fatalf("expected the finished drink to carry a Take cup row, got %d rows", len(finalComps))
	}
	row, ok := finalComps[0].(discordgo.ActionsRow)
	if !ok || len(row.Components) != 1 {
		t.Fatalf("take cup row malformed: %+v", finalComps[0])
	}
	btn, ok := row.Components[0].(discordgo.Button)
	if !ok || !strings.HasPrefix(btn.CustomID, takeCupPrefix) {
		t.Errorf("expected a Take cup button, got %+v", row.Components[0])
	}
}

// --- Part-service detection & slacker mechanic ------------------------------

func TestMaxPartDemand(t *testing.T) {
	if g := maxPartDemand(partGrounds); g != 36 { // flat white
		t.Errorf("max grounds demand = %d, want 36", g)
	}
	if w := maxPartDemand("water"); w != 200 { // hot water
		t.Errorf("max water demand = %d, want 200", w)
	}
	if mk := maxPartDemand("milk"); mk != 180 { // latte macchiato
		t.Errorf("max milk demand = %d, want 180", mk)
	}
}

func TestPartsNeedingService(t *testing.T) {
	full := MachineInventory{BeansMildGrams: maxBeansMildG, BeansEspressoGrams: maxBeansEspressoG, WaterMl: maxWaterMl, MilkMl: maxMilkMl, GroundsGrams: 0}
	if parts := partsNeedingService(full); len(parts) != 0 {
		t.Errorf("a full machine needs no service, got %v", parts)
	}
	low := MachineInventory{BeansMildGrams: 0, BeansEspressoGrams: maxBeansEspressoG, WaterMl: maxWaterMl, MilkMl: maxMilkMl, GroundsGrams: maxGroundsG}
	parts := partsNeedingService(low)
	if !slices.Contains(parts, "beans_mild") || !slices.Contains(parts, partGrounds) {
		t.Errorf("expected beans_mild and grounds to need service, got %v", parts)
	}
	if slices.Contains(parts, "water") {
		t.Errorf("water was full, should not need service: %v", parts)
	}
}

func pendingServiceUser(m *Module, t *testing.T, guildID, part string) (string, bool) {
	t.Helper()
	var ps PendingService
	err := m.getDB().Where("guild_id = ? AND part = ?", guildID, part).First(&ps).Error
	if err != nil {
		return "", false
	}
	return ps.UserID, true
}

func TestDispenseLeavesServiceNeededAndPending(t *testing.T) {
	m := newTestModule(t)
	// Just enough mild beans for one coffee, leaving 9g (< 11g demand) after.
	setLevels(m, t, "g1", func(inv *MachineInventory) { inv.BeansMildGrams = 20 })

	out, err := m.dispense("g1", "alice", "coffee", false, false)
	if err != nil || !out.ok {
		t.Fatalf("dispense: err=%v fail=%q", err, out.failMsg)
	}
	if !slices.Contains(out.serviceNeeded, "beans_mild") {
		t.Errorf("serviceNeeded = %v, want beans_mild", out.serviceNeeded)
	}
	if u, ok := pendingServiceUser(m, t, "g1", "beans_mild"); !ok || u != "alice" {
		t.Errorf("pending beans_mild user = %q (ok=%v), want alice", u, ok)
	}
}

func TestSlackerBlamedWhenNextUserBlocked(t *testing.T) {
	m := newTestModule(t)
	// Exactly one coffee's worth of water, so alice's brew empties the tank.
	setLevels(m, t, "g1", func(inv *MachineInventory) { inv.WaterMl = 120 })

	a, err := m.dispense("g1", "alice", "coffee", false, false)
	if err != nil || !a.ok {
		t.Fatalf("alice brew: err=%v fail=%q", err, a.failMsg)
	}
	if !slices.Contains(a.serviceNeeded, "water") {
		t.Fatalf("alice should have been nudged about water, got %v", a.serviceNeeded)
	}

	// bob is now blocked and forced to refill; alice is to blame.
	b, err := m.dispense("g1", "bob", "coffee", false, false)
	if err != nil {
		t.Fatalf("bob brew: %v", err)
	}
	if b.ok {
		t.Fatal("bob should be blocked on empty water")
	}
	if b.blamedUserID != "alice" || b.blamedPart != "water" {
		t.Errorf("blame = %q/%q, want alice/water", b.blamedUserID, b.blamedPart)
	}
	if c := countSlackers(m, t, "g1", "alice", "water"); c != 1 {
		t.Errorf("alice should have 1 water slacker event, got %d", c)
	}
	// pending cleared, so a retry does not double-blame.
	if _, ok := pendingServiceUser(m, t, "g1", "water"); ok {
		t.Error("pending water should be cleared after blame")
	}
	b2, _ := m.dispense("g1", "bob", "coffee", false, false)
	if b2.blamedUserID != "" {
		t.Errorf("retry should not re-blame, got %q", b2.blamedUserID)
	}
	if c := countSlackers(m, t, "g1", "alice", "water"); c != 1 {
		t.Errorf("retry should not add another slacker event, got %d", c)
	}
}

func TestNoSelfBlame(t *testing.T) {
	m := newTestModule(t)
	setLevels(m, t, "g1", func(inv *MachineInventory) { inv.WaterMl = 120 })

	if a, err := m.dispense("g1", "alice", "coffee", false, false); err != nil || !a.ok {
		t.Fatalf("alice brew: err=%v fail=%q", err, a.failMsg)
	}
	// alice herself is blocked next; she shouldn't be blamed for her own mess.
	a2, _ := m.dispense("g1", "alice", "coffee", false, false)
	if a2.ok {
		t.Fatal("expected block on empty water")
	}
	if a2.blamedUserID != "" {
		t.Errorf("no self-blame expected, got %q", a2.blamedUserID)
	}
	if c := countSlackers(m, t, "g1", "alice", "water"); c != 0 {
		t.Errorf("self-block should record no slacker event, got %d", c)
	}
}

func TestRefillClearsPendingService(t *testing.T) {
	m := newTestModule(t)
	setLevels(m, t, "g1", func(inv *MachineInventory) { inv.WaterMl = 120 })
	if a, err := m.dispense("g1", "alice", "coffee", false, false); err != nil || !a.ok {
		t.Fatalf("alice brew: err=%v fail=%q", err, a.failMsg)
	}
	if _, ok := pendingServiceUser(m, t, "g1", "water"); !ok {
		t.Fatal("expected pending water after alice's brew")
	}

	if _, err := m.refill("g1", "carol", "water"); err != nil {
		t.Fatalf("refill: %v", err)
	}
	if _, ok := pendingServiceUser(m, t, "g1", "water"); ok {
		t.Error("refilling water should clear the pending-service record")
	}
	// bob now brews fine and nobody is blamed.
	b, _ := m.dispense("g1", "bob", "coffee", false, false)
	if !b.ok || b.blamedUserID != "" {
		t.Errorf("after refill bob should brew with no blame: ok=%v blame=%q", b.ok, b.blamedUserID)
	}
}

func TestEmptyClearsPendingGrounds(t *testing.T) {
	m := newTestModule(t)
	// One coffee away from a full grounds container (coffee adds 20g).
	setLevels(m, t, "g1", func(inv *MachineInventory) { inv.GroundsGrams = maxGroundsG - 20 })
	a, err := m.dispense("g1", "alice", "coffee", false, false)
	if err != nil || !a.ok {
		t.Fatalf("alice brew: err=%v fail=%q", err, a.failMsg)
	}
	if !slices.Contains(a.serviceNeeded, partGrounds) {
		t.Fatalf("alice should be nudged about grounds, got %v", a.serviceNeeded)
	}
	if _, err := m.emptyGrounds("g1", "carol"); err != nil {
		t.Fatalf("empty: %v", err)
	}
	if _, ok := pendingServiceUser(m, t, "g1", partGrounds); ok {
		t.Error("emptying grounds should clear the pending-service record")
	}
}

func countSlackers(m *Module, t *testing.T, guildID, userID, part string) int64 {
	t.Helper()
	var c int64
	if err := m.getDB().Model(&SlackerEvent{}).
		Where("guild_id = ? AND user_id = ? AND part = ?", guildID, userID, part).
		Count(&c).Error; err != nil {
		t.Fatalf("count slackers: %v", err)
	}
	return c
}

func TestServiceHint(t *testing.T) {
	if serviceHint(nil) != "" {
		t.Error("no parts should yield no hint")
	}
	one := serviceHint([]string{"water"})
	if !strings.Contains(one, "water") || !strings.Contains(one, "/coffeemachine refill") {
		t.Errorf("single-part hint wrong: %q", one)
	}
	grounds := serviceHint([]string{partGrounds})
	if !strings.Contains(grounds, "grounds container") || !strings.Contains(grounds, "/coffeemachine empty") {
		t.Errorf("grounds hint should suggest empty: %q", grounds)
	}
	multi := serviceHint([]string{"water", "milk"})
	if !strings.Contains(multi, "water and milk") || !strings.Contains(multi, "are running low") {
		t.Errorf("multi-part hint wrong: %q", multi)
	}
}

func TestBlockedFallbackWithBlame(t *testing.T) {
	out := dispenseOutcome{failMsg: outOfMsg("water", "water"), blamedUserID: "alice", blamedPart: "water"}
	got := blockedFallback(out)
	if !strings.Contains(got, "<@alice>") || !strings.Contains(got, "water") {
		t.Errorf("blame fallback should mention the slacker and part: %q", got)
	}
	noBlame := blockedFallback(dispenseOutcome{failMsg: "oops"})
	if noBlame != "oops" {
		t.Errorf("no-blame fallback should be the plain message, got %q", noBlame)
	}
}

func TestExecuteBrewAppendsServiceHint(t *testing.T) {
	m := newTestModule(t)
	stubLLM(m, t, "", nil)
	_, edits, _ := captureBrewIO(m)
	setLevels(m, t, "g1", func(inv *MachineInventory) { inv.WaterMl = 120 })

	m.handleCoffeeInteraction(nil, makeBrewInteraction("g1", strOpt("drink", "coffee")))

	if len(*edits) != 1 || !strings.Contains((*edits)[0], "Heads up") {
		t.Fatalf("final brew message should carry the service nudge, got %v", *edits)
	}
}

func TestBlockedBrewMentionsSlacker(t *testing.T) {
	m := newTestModule(t)
	stubLLM(m, t, "", nil) // deterministic fallback keeps the mention intact
	setLevels(m, t, "g1", func(inv *MachineInventory) { inv.WaterMl = 120 })
	if _, err := m.dispense("g1", "alice", "coffee", false, false); err != nil {
		t.Fatalf("alice brew: %v", err)
	}

	resp, _, _ := captureBrewIO(m)
	bob := makeBrewInteraction("g1", strOpt("drink", "coffee"))
	bob.Member.User.ID = "bob"
	m.handleCoffeeInteraction(nil, bob)

	if len(*resp) != 1 || !(*resp)[0].ephemeral {
		t.Fatalf("expected 1 ephemeral blocked response, got %+v", *resp)
	}
	if !strings.Contains((*resp)[0].content, "<@alice>") {
		t.Errorf("blocked message should name the slacker: %q", (*resp)[0].content)
	}
}

// --- Detailed per-user stats -------------------------------------------------

func TestTopGroundsEmptiers(t *testing.T) {
	m := newTestModule(t)
	d := m.getDB()
	d.Create(&RefillEvent{GuildID: "g1", UserID: "A", Part: partGrounds, Amount: 200})
	d.Create(&RefillEvent{GuildID: "g1", UserID: "A", Part: partGrounds, Amount: 280})
	d.Create(&RefillEvent{GuildID: "g1", UserID: "B", Part: partGrounds, Amount: 100})
	d.Create(&RefillEvent{GuildID: "g1", UserID: "A", Part: "water", Amount: 500}) // not a grounds empty

	rows, err := m.topGroundsEmptiers("g1", 5)
	if err != nil {
		t.Fatalf("topGroundsEmptiers: %v", err)
	}
	if len(rows) != 2 || rows[0].UserID != "A" || rows[0].Count != 2 || rows[0].TotalGrams != 480 {
		t.Fatalf("rows = %+v, want A first with 2×/480g", rows)
	}
	if avgGrams(rows[0].TotalGrams, rows[0].Count) != 240 {
		t.Errorf("avg = %d, want 240", avgGrams(rows[0].TotalGrams, rows[0].Count))
	}
}

func TestUserStatsBreakdowns(t *testing.T) {
	m := newTestModule(t)
	d := m.getDB()
	d.Create(&DrinkEvent{GuildID: "g1", UserID: "A", Drink: "coffee"})
	d.Create(&DrinkEvent{GuildID: "g1", UserID: "A", Drink: "coffee"})
	d.Create(&DrinkEvent{GuildID: "g1", UserID: "A", Drink: "espresso"})
	d.Create(&RefillEvent{GuildID: "g1", UserID: "A", Part: "water", Amount: 500})
	d.Create(&RefillEvent{GuildID: "g1", UserID: "A", Part: "water", Amount: 300})
	d.Create(&RefillEvent{GuildID: "g1", UserID: "A", Part: partGrounds, Amount: 250})
	d.Create(&SlackerEvent{GuildID: "g1", UserID: "A", Part: "milk"})

	drinks, _ := m.userDrinkBreakdown("g1", "A")
	if len(drinks) != 2 || drinks[0].Key != "coffee" || drinks[0].Count != 2 {
		t.Errorf("drink breakdown = %+v", drinks)
	}
	refills, _ := m.userRefillBreakdown("g1", "A")
	if len(refills) != 1 || refills[0].Key != "water" || refills[0].Count != 2 || refills[0].Amount != 800 {
		t.Errorf("refill breakdown = %+v (grounds must be excluded)", refills)
	}
	gc, gt, _ := m.userGroundsStats("g1", "A")
	if gc != 1 || gt != 250 {
		t.Errorf("grounds stats = %d×/%dg, want 1/250", gc, gt)
	}
	slack, _ := m.userSlackerBreakdown("g1", "A")
	if len(slack) != 1 || slack[0].Key != "milk" || slack[0].Count != 1 {
		t.Errorf("slacker breakdown = %+v", slack)
	}
}

func TestFormatUserStats(t *testing.T) {
	got := formatUserStats("A",
		[]labelCount{{Key: "coffee", Count: 2}, {Key: "espresso", Count: 1}},
		[]labelCount{{Key: "water", Count: 2, Amount: 800}},
		1, 250,
		[]labelCount{{Key: "milk", Count: 3}})
	for _, want := range []string{"<@A>", "Coffee: 2", "Espresso: 1", "Water: 2× (800 total)", "1× · 250g total · 250g avg", "Milk: 3"} {
		if !strings.Contains(got, want) {
			t.Errorf("stats missing %q:\n%s", want, got)
		}
	}
}

func TestFormatUserStats_Empty(t *testing.T) {
	got := formatUserStats("A", nil, nil, 0, 0, nil)
	if !strings.Contains(got, "Grounds emptied:** never") {
		t.Errorf("empty grounds should read 'never': %q", got)
	}
	if strings.Contains(got, "Slacker misses") {
		t.Errorf("no slacker section when clean: %q", got)
	}
}

func TestTopSlackers(t *testing.T) {
	m := newTestModule(t)
	d := m.getDB()
	d.Create(&SlackerEvent{GuildID: "g1", UserID: "A", Part: "water"})
	d.Create(&SlackerEvent{GuildID: "g1", UserID: "A", Part: "milk"})
	d.Create(&SlackerEvent{GuildID: "g1", UserID: "B", Part: partGrounds})
	d.Create(&SlackerEvent{GuildID: "other", UserID: "Z", Part: "water"})

	rows, err := m.topSlackers("g1", 5)
	if err != nil {
		t.Fatalf("topSlackers: %v", err)
	}
	if len(rows) != 2 || rows[0].UserID != "A" || rows[0].Count != 2 {
		t.Errorf("topSlackers = %+v, want A(2) then B(1)", rows)
	}
}
