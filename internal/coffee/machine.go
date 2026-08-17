package coffee

import (
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/bwmarrin/discordgo"
	"gorm.io/gorm"
)

// Tank and hopper capacities for the bean-to-cup machine. Metric units: beans
// and grounds in grams, water and milk in milliliters.
const (
	maxBeansMildG     = 1000
	maxBeansEspressoG = 1000
	maxWaterMl        = 2000
	maxMilkMl         = 1000
	maxGroundsG       = 500

	// addMilkMl is the splash of milk added when a black drink opts into milk.
	addMilkMl = 40

	// partGrounds is the RefillEvent.Part value denoting a grounds-empty action.
	partGrounds = "grounds"

	// Tea bag stock levels per variety. Seeded at first use; capped at max.
	maxTeaBagsPerFlavor  = 50
	seedTeaBagsPerFlavor = 20
)

type beanType int

const (
	beanNone beanType = iota
	beanMild
	beanEspresso
)

// recipe describes what a single drink consumes and produces.
type recipe struct {
	key        string // slash-command choice value
	label      string // human-facing name
	bean       beanType
	beanGrams  int
	waterMl    int
	milkMl     int  // milk built into the drink (latte, flat white, ...)
	groundsG   int  // spent grounds produced
	allowsMilk bool // may take an optional milk splash
	brewSecs   int  // simulated brew time, varies by drink
}

// coffeeRecipes are the espresso-machine drinks. The first entry is the default
// for /brew when no drink is specified.
var coffeeRecipes = []recipe{
	{key: "coffee", label: "Coffee", bean: beanMild, beanGrams: 11, waterMl: 120, groundsG: 20, allowsMilk: true, brewSecs: 28},
	{key: "espresso", label: "Espresso", bean: beanEspresso, beanGrams: 9, waterMl: 40, groundsG: 18, allowsMilk: true, brewSecs: 24},
	{key: "milk_coffee", label: "Milk coffee", bean: beanMild, beanGrams: 11, waterMl: 80, milkMl: 120, groundsG: 20, brewSecs: 32},
	{key: "latte_macchiato", label: "Latte macchiato", bean: beanEspresso, beanGrams: 9, waterMl: 40, milkMl: 180, groundsG: 18, brewSecs: 36},
	{key: "flat_white", label: "Flat white", bean: beanEspresso, beanGrams: 18, waterMl: 60, milkMl: 120, groundsG: 36, brewSecs: 40},
	{key: "cappuccino", label: "Cappuccino", bean: beanEspresso, beanGrams: 9, waterMl: 40, milkMl: 120, groundsG: 18, brewSecs: 34},
}

// teaFlavor describes a tracked tea variety. Each consumes 1 tea bag and 200 ml
// water per brew and is stored in TeaBagInventory keyed by flavor.key.
type teaFlavor struct {
	key   string
	label string
}

var teaFlavors = []teaFlavor{
	{key: "black", label: "Black"},
	{key: "green", label: "Green"},
	{key: "earl_grey", label: "Earl Grey"},
	{key: "peppermint", label: "Peppermint"},
	{key: "chamomile", label: "Chamomile"},
	{key: "rooibos", label: "Rooibos"},
	{key: "fennel", label: "Fennel"},
	{key: "assam", label: "Assam"},
}

// teaFlavorLabel returns the display label for a tea flavor key, or the key
// itself when unknown.
func teaFlavorLabel(flavor string) string {
	for _, t := range teaFlavors {
		if t.key == flavor {
			return t.label
		}
	}
	return flavor
}

// menu is the full ordered drink list: coffee recipes followed by tea recipes.
// Built in init so tea recipes are derived from teaFlavors.
var menu []recipe

// refillParts are the machine tanks/hoppers refillable via /coffeemachine refill.
// teaRefillParts are appended in init from teaFlavors.
var (
	machineRefillParts = []refillPart{
		{key: "beans_mild", label: "Mild beans", max: maxBeansMildG, unit: "g"},
		{key: "beans_espresso", label: "Espresso beans", max: maxBeansEspressoG, unit: "g"},
		{key: "water", label: "Water", max: maxWaterMl, unit: "ml"},
		{key: "milk", label: "Milk", max: maxMilkMl, unit: "ml"},
	}
	teaRefillParts []refillPart
	refillParts    []refillPart // machineRefillParts + teaRefillParts, built in init
)

func init() {
	// Build full drink menu: coffee recipes + one recipe per tea flavor.
	menu = make([]recipe, len(coffeeRecipes))
	copy(menu, coffeeRecipes)
	for _, t := range teaFlavors {
		menu = append(menu, recipe{
			key:        "tea_" + t.key,
			label:      t.label + " tea",
			waterMl:    200,
			allowsMilk: true,
			brewSecs:   20,
		})
	}

	// Build tea refill parts list.
	for _, t := range teaFlavors {
		teaRefillParts = append(teaRefillParts, refillPart{
			key:   "tea_" + t.key,
			label: "Tea bags (" + t.label + ")",
			max:   maxTeaBagsPerFlavor,
			unit:  " bags",
		})
	}
	refillParts = append(machineRefillParts, teaRefillParts...)
}

// refillPart describes a refillable tank/hopper or tea bag variety.
type refillPart struct {
	key   string // choice value, also RefillEvent.Part
	label string
	max   int
	unit  string // "g", "ml", or " bags"
}

func refillPartByKey(key string) (refillPart, bool) {
	for _, p := range refillParts {
		if p.key == key {
			return p, true
		}
	}
	return refillPart{}, false
}

// brewTime is how long the machine pretends to take dispensing a drink.
func brewTime(r recipe) time.Duration {
	return time.Duration(r.brewSecs) * time.Second
}

func recipeByKey(key string) (recipe, bool) {
	for _, r := range menu {
		if r.key == key {
			return r, true
		}
	}
	return recipe{}, false
}

// partLabel returns a human-facing name for a refillable part or the grounds
// container.
func partLabel(key string) string {
	if key == partGrounds {
		return "grounds container"
	}
	if flavor, ok := strings.CutPrefix(key, "tea_"); ok {
		return strings.ToLower(teaFlavorLabel(flavor)) + " tea bags"
	}
	if p, ok := refillPartByKey(key); ok {
		return strings.ToLower(p.label)
	}
	return key
}

// maxPartDemand returns the largest amount of the given part a single drink can
// consume across the whole menu (milk includes the worst-case optional splash).
// It is the threshold below which the next brew of some drink could be blocked.
func maxPartDemand(part string) int {
	max := 0
	for _, r := range menu {
		v := 0
		switch part {
		case "beans_mild":
			if r.bean == beanMild {
				v = r.beanGrams
			}
		case "beans_espresso":
			if r.bean == beanEspresso {
				v = r.beanGrams
			}
		case "water":
			v = r.waterMl
		case "milk":
			v = r.milkMl
			if r.allowsMilk {
				v += addMilkMl
			}
		case partGrounds:
			v = r.groundsG
		}
		if v > max {
			max = v
		}
	}
	return max
}

// partsNeedingService reports which parts the given inventory has left low (or,
// for grounds, too full) enough that the next brew of some drink would be
// blocked. The order matches the machine status display.
func partsNeedingService(inv MachineInventory) []string {
	var parts []string
	if inv.BeansMildGrams < maxPartDemand("beans_mild") {
		parts = append(parts, "beans_mild")
	}
	if inv.BeansEspressoGrams < maxPartDemand("beans_espresso") {
		parts = append(parts, "beans_espresso")
	}
	if inv.WaterMl < maxPartDemand("water") {
		parts = append(parts, "water")
	}
	if inv.MilkMl < maxPartDemand("milk") {
		parts = append(parts, "milk")
	}
	if inv.GroundsGrams+maxPartDemand(partGrounds) > maxGroundsG {
		parts = append(parts, partGrounds)
	}
	return parts
}

// seedInventoryTx loads the guild's inventory, creating a full machine on first
// use. Works on any *gorm.DB (a live handle or an open transaction).
func seedInventoryTx(db *gorm.DB, guildID string) (MachineInventory, error) {
	var inv MachineInventory
	err := db.Where(MachineInventory{GuildID: guildID}).
		Attrs(MachineInventory{
			BeansMildGrams:     maxBeansMildG,
			BeansEspressoGrams: maxBeansEspressoG,
			WaterMl:            maxWaterMl,
			MilkMl:             maxMilkMl,
			GroundsGrams:       0,
		}).
		FirstOrCreate(&inv).Error
	return inv, err
}

// getOrSeedInventory returns the guild's inventory, creating a full machine on
// first use. Read-only callers (status) use this directly.
func (m *Module) getOrSeedInventory(guildID string) (MachineInventory, error) {
	d := m.getDB()
	if d == nil {
		return MachineInventory{}, errors.New("store not initialized")
	}
	return seedInventoryTx(d, guildID)
}

// dispenseOutcome is the result of attempting to brew one drink.
type dispenseOutcome struct {
	recipe       recipe
	inventory    MachineInventory
	ok           bool
	failMsg      string // user-facing reason when ok is false
	splashMilk   bool   // an optional milk splash was added to a black drink
	withSugar    bool
	order        DrinkOrder
	blockedUntil time.Time

	// serviceNeeded lists parts this (successful) brew left low/full enough that
	// the next brew could be blocked; the brewer is nudged to refill/empty them.
	serviceNeeded []string

	// blamedUserID and blamedPart name the previous brewer who left the blocking
	// part empty/full and never serviced it, when this brew was blocked. Empty
	// when there is no one to blame.
	blamedUserID string
	blamedPart   string
}

// dispense brews one drink for userID in guildID, deducting consumables and
// recording a DrinkEvent. On insufficient stock (or a full grounds container)
// it returns ok=false with a user-facing reason and mutates nothing.
func (m *Module) dispense(guildID, userID, drinkKey string, addMilk, addSugar bool) (dispenseOutcome, error) {
	r, found := recipeByKey(drinkKey)
	if !found {
		return dispenseOutcome{failMsg: fmt.Sprintf("Unknown drink %q.", drinkKey)}, nil
	}
	d := m.getDB()
	if d == nil {
		return dispenseOutcome{recipe: r}, errors.New("store not initialized")
	}

	splashMilk := addMilk && r.allowsMilk
	milkNeeded := r.milkMl
	if splashMilk {
		milkNeeded += addMilkMl
	}
	withMilk := milkNeeded > 0

	out := dispenseOutcome{recipe: r, splashMilk: splashMilk, withSugar: addSugar}

	// For tea drinks, extract the flavor key so we can check/deduct tea bags.
	teaBagFlavor, isTea := strings.CutPrefix(drinkKey, "tea_")
	if !isTea {
		teaBagFlavor = ""
	}

	m.machineMu.Lock()
	defer m.machineMu.Unlock()

	err := d.Transaction(func(tx *gorm.DB) error {
		now := m.nowFunc().UTC()
		var dueOrders []DrinkOrder
		if e := tx.Where("user_id = ? AND status = ?", userID, orderStatusReady).
			Find(&dueOrders).Error; e != nil {
			return e
		}
		for idx := range dueOrders {
			if _, e := expireOrderTx(tx, &dueOrders[idx], now); e != nil {
				return e
			}
		}
		status, e := restrictionStatusTx(tx, userID, now)
		if e != nil {
			return e
		}
		if status.blocked(now) {
			out.blockedUntil = status.BlockedUntil
			out.failMsg = formatRestriction(status.BlockedUntil, now)
			return nil
		}
		var openOrders int64
		if e = tx.Model(&DrinkOrder{}).
			Where("guild_id = ? AND user_id = ? AND status IN ?", guildID, userID, []string{orderStatusBrewing, orderStatusReady}).
			Count(&openOrders).Error; e != nil {
			return e
		}
		if openOrders > 0 {
			out.failMsg = "You already have a drink waiting. Pick it up before using `/brew` again."
			return nil
		}
		inv, e := seedInventoryTx(tx, guildID)
		if e != nil {
			return e
		}

		// Load tea bag inventory up front so we can check stock in the switch below.
		var teaBag *TeaBagInventory
		if teaBagFlavor != "" {
			tb, e2 := getOrSeedTeaBagTx(tx, guildID, teaBagFlavor)
			if e2 != nil {
				return e2
			}
			teaBag = &tb
		}

		blockPart := ""
		switch {
		case r.bean == beanMild && inv.BeansMildGrams < r.beanGrams:
			out.failMsg, blockPart = outOfMsg("mild beans", "beans_mild"), "beans_mild"
		case r.bean == beanEspresso && inv.BeansEspressoGrams < r.beanGrams:
			out.failMsg, blockPart = outOfMsg("espresso beans", "beans_espresso"), "beans_espresso"
		case inv.WaterMl < r.waterMl:
			out.failMsg, blockPart = outOfMsg("water", "water"), "water"
		case inv.MilkMl < milkNeeded:
			out.failMsg, blockPart = outOfMsg("milk", "milk"), "milk"
		case inv.GroundsGrams+r.groundsG > maxGroundsG:
			out.failMsg, blockPart = "The grounds container is full. Empty it with `/coffeemachine empty`.", partGrounds
		case teaBag != nil && teaBag.Count < 1:
			partKey := "tea_" + teaBagFlavor
			out.failMsg = outOfMsg(teaFlavorLabel(teaBagFlavor)+" tea bags", partKey)
			blockPart = partKey
		}
		if out.failMsg != "" {
			out.inventory = inv
			// The next user is now forced to service blockPart. If a previous
			// brewer left it that way and never fixed it, blame them once.
			if blockPart != "" {
				if e = m.blameSlackerTx(tx, guildID, blockPart, userID, &out); e != nil {
					return e
				}
			}
			return nil // no inventory change; caller sees ok=false
		}

		switch r.bean {
		case beanMild:
			inv.BeansMildGrams -= r.beanGrams
		case beanEspresso:
			inv.BeansEspressoGrams -= r.beanGrams
		}
		inv.WaterMl -= r.waterMl
		inv.MilkMl -= milkNeeded
		inv.GroundsGrams += r.groundsG

		if e = tx.Save(&inv).Error; e != nil {
			return e
		}

		// Deduct one tea bag and flag service if now empty.
		if teaBag != nil {
			teaBag.Count--
			if e = tx.Save(teaBag).Error; e != nil {
				return e
			}
		}

		if e = tx.Create(&DrinkEvent{
			GuildID:   guildID,
			UserID:    userID,
			Drink:     r.key,
			WithMilk:  withMilk,
			WithSugar: addSugar,
		}).Error; e != nil {
			return e
		}
		out.order = DrinkOrder{
			GuildID: guildID, UserID: userID, Drink: r.key, Status: orderStatusBrewing,
			ReadyAt: now.Add(brewTime(r)),
		}
		if e = tx.Create(&out.order).Error; e != nil {
			return e
		}
		// Record which parts this brew left needing service and pin the brewer as
		// responsible, so a later blocked brew can blame them.
		out.serviceNeeded = partsNeedingService(inv)
		if teaBag != nil && teaBag.Count == 0 {
			out.serviceNeeded = append(out.serviceNeeded, "tea_"+teaBagFlavor)
		}
		for _, p := range out.serviceNeeded {
			if e = setPendingServiceTx(tx, guildID, p, userID); e != nil {
				return e
			}
		}
		out.inventory = inv
		out.ok = true
		return nil
	})
	if err != nil {
		return dispenseOutcome{recipe: r}, err
	}
	return out, nil
}

func outOfMsg(name, partKey string) string {
	return fmt.Sprintf("Out of %s. Top it up with `/coffeemachine refill part:%s`.", name, partKey)
}

// blameSlackerTx handles a brew blocked on blockPart. If a previous brewer was
// pinned as responsible for that part (and is not the now-blocked user), it
// records a SlackerEvent against them and stores the blame on out. The pending
// record is always cleared: the now-blocked user will have to service the part,
// so the episode is resolved either way.
func (m *Module) blameSlackerTx(tx *gorm.DB, guildID, blockPart, blockedUserID string, out *dispenseOutcome) error {
	var ps PendingService
	err := tx.Where("guild_id = ? AND part = ?", guildID, blockPart).First(&ps).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if ps.UserID != "" && ps.UserID != blockedUserID {
		if e := tx.Create(&SlackerEvent{GuildID: guildID, UserID: ps.UserID, Part: blockPart}).Error; e != nil {
			return e
		}
		out.blamedUserID = ps.UserID
		out.blamedPart = blockPart
	}
	return clearPendingServiceTx(tx, guildID, blockPart)
}

// refillOutcome is the result of a refill attempt.
type refillOutcome struct {
	part        refillPart
	added       int
	inventory   MachineInventory
	alreadyFull bool
}

// refill tops the named tank/hopper to its maximum and records a RefillEvent for
// the amount added. A full tank is a no-op (alreadyFull=true).
func (m *Module) refill(guildID, userID, partKey string) (refillOutcome, error) {
	p, found := refillPartByKey(partKey)
	if !found {
		return refillOutcome{}, fmt.Errorf("unknown part %q", partKey)
	}
	d := m.getDB()
	if d == nil {
		return refillOutcome{}, errors.New("store not initialized")
	}

	out := refillOutcome{part: p}

	m.machineMu.Lock()
	defer m.machineMu.Unlock()

	err := d.Transaction(func(tx *gorm.DB) error {
		inv, e := seedInventoryTx(tx, guildID)
		if e != nil {
			return e
		}
		// The part is being serviced; nobody is on the hook for it anymore.
		if e = clearPendingServiceTx(tx, guildID, p.key); e != nil {
			return e
		}
		var cur *int
		switch p.key {
		case "beans_mild":
			cur = &inv.BeansMildGrams
		case "beans_espresso":
			cur = &inv.BeansEspressoGrams
		case "water":
			cur = &inv.WaterMl
		case "milk":
			cur = &inv.MilkMl
		}
		added := p.max - *cur
		if added <= 0 {
			out.alreadyFull = true
			out.inventory = inv
			return nil
		}
		*cur = p.max
		if e = tx.Save(&inv).Error; e != nil {
			return e
		}
		if e = tx.Create(&RefillEvent{
			GuildID: guildID,
			UserID:  userID,
			Part:    p.key,
			Amount:  added,
		}).Error; e != nil {
			return e
		}
		out.added = added
		out.inventory = inv
		return nil
	})
	return out, err
}

// emptyOutcome is the result of an empty-grounds attempt.
type emptyOutcome struct {
	removed      int
	inventory    MachineInventory
	alreadyEmpty bool
}

// emptyGrounds empties the grounds container and records a RefillEvent for the
// amount removed. An empty container is a no-op (alreadyEmpty=true).
func (m *Module) emptyGrounds(guildID, userID string) (emptyOutcome, error) {
	d := m.getDB()
	if d == nil {
		return emptyOutcome{}, errors.New("store not initialized")
	}

	var out emptyOutcome

	m.machineMu.Lock()
	defer m.machineMu.Unlock()

	err := d.Transaction(func(tx *gorm.DB) error {
		inv, e := seedInventoryTx(tx, guildID)
		if e != nil {
			return e
		}
		// The grounds are being serviced; nobody is on the hook anymore.
		if e = clearPendingServiceTx(tx, guildID, partGrounds); e != nil {
			return e
		}
		if inv.GroundsGrams <= 0 {
			out.alreadyEmpty = true
			out.inventory = inv
			return nil
		}
		removed := inv.GroundsGrams
		inv.GroundsGrams = 0
		if e = tx.Save(&inv).Error; e != nil {
			return e
		}
		if e = tx.Create(&RefillEvent{
			GuildID: guildID,
			UserID:  userID,
			Part:    partGrounds,
			Amount:  removed,
		}).Error; e != nil {
			return e
		}
		out.removed = removed
		out.inventory = inv
		return nil
	})
	return out, err
}

func percent(cur, max int) int {
	if max <= 0 {
		return 0
	}
	return int(math.Round(float64(cur) / float64(max) * 100))
}

// drinkLabel is the display name for a served drink.
func drinkLabel(r recipe) string { return r.label }

// drinkEmoji picks the cup emoji for a served drink.
func drinkEmoji(r recipe) string {
	if strings.HasPrefix(r.key, "tea_") {
		return "🍵"
	}
	return "☕"
}

// extrasSuffix renders the " with milk and sugar" trailer, empty when neither.
func extrasSuffix(splashMilk, withSugar bool) string {
	extras := []string{}
	if splashMilk {
		extras = append(extras, "milk")
	}
	if withSugar {
		extras = append(extras, "sugar")
	}
	if len(extras) == 0 {
		return ""
	}
	return " with " + strings.Join(extras, " and ")
}

// formatDispenseSuccess builds the deterministic fallback confirmation for a
// served drink (no machine stats — those live in /coffeemachine status).
func formatDispenseSuccess(r recipe, splashMilk, withSugar bool) string {
	return fmt.Sprintf("%s Here's your %s%s!", drinkEmoji(r), drinkLabel(r), extrasSuffix(splashMilk, withSugar))
}

// humanJoin renders a slice as "a", "a and b", or "a, b and c".
func humanJoin(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	case 2:
		return items[0] + " and " + items[1]
	default:
		return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
	}
}

// serviceHint renders the nudge appended to a brew confirmation when the brew
// left parts needing service, naming the parts and the fixing commands. Empty
// when nothing needs service.
func serviceHint(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	labels := make([]string, 0, len(parts))
	emptyGrounds := false
	for _, p := range parts {
		labels = append(labels, partLabel(p))
		if p == partGrounds {
			emptyGrounds = true
		}
	}
	verb := "is"
	if len(labels) > 1 {
		verb = "are"
	}
	action := "refill with `/coffeemachine refill`"
	switch {
	case emptyGrounds && len(labels) == 1:
		action = "empty it with `/coffeemachine empty`"
	case emptyGrounds:
		action = "refill/empty with `/coffeemachine`"
	}
	return fmt.Sprintf("\n\n⚠️ Heads up: the %s %s running low — please %s so the next person isn't left stranded.",
		humanJoin(labels), verb, action)
}

// blockedFallback builds the user-facing reason a brew was blocked, naming the
// previous brewer to blame when one was recorded.
func blockedFallback(out dispenseOutcome) string {
	msg := out.failMsg
	if out.blamedUserID != "" {
		msg += fmt.Sprintf(" <@%s> used the last of the %s and never refilled it — looks like it's on you now.",
			out.blamedUserID, partLabel(out.blamedPart))
	}
	return msg
}

// formatStatus renders the machine status, levels, and stat leaderboards. The
// per-drink and per-part breakdowns live in /coffeemachine stats; this view
// keeps one headline number per leaderboard.
func formatStatus(inv MachineInventory, drinkers, refillers []userCount, emptiers []groundsEmptier, slackers []userCount, teaBags []TeaBagInventory) string {
	var sb strings.Builder
	sb.WriteString("☕ **Coffee machine status**\n")
	fmt.Fprintf(&sb, "Mild beans: %d/%dg (%d%%)\n", inv.BeansMildGrams, maxBeansMildG, percent(inv.BeansMildGrams, maxBeansMildG))
	fmt.Fprintf(&sb, "Espresso beans: %d/%dg (%d%%)\n", inv.BeansEspressoGrams, maxBeansEspressoG, percent(inv.BeansEspressoGrams, maxBeansEspressoG))
	fmt.Fprintf(&sb, "Water: %d/%dml (%d%%)\n", inv.WaterMl, maxWaterMl, percent(inv.WaterMl, maxWaterMl))
	fmt.Fprintf(&sb, "Milk: %d/%dml (%d%%)\n", inv.MilkMl, maxMilkMl, percent(inv.MilkMl, maxMilkMl))
	fmt.Fprintf(&sb, "Grounds: %d/%dg (%d%%)\n", inv.GroundsGrams, maxGroundsG, percent(inv.GroundsGrams, maxGroundsG))

	if len(teaBags) > 0 {
		sb.WriteString("\n**Tea bags**\n")
		for _, tb := range teaBags {
			fmt.Fprintf(&sb, "🍵 %s: %d/%d bags (%d%%)\n",
				teaFlavorLabel(tb.Flavor)+" tea", tb.Count, maxTeaBagsPerFlavor,
				percent(tb.Count, maxTeaBagsPerFlavor))
		}
	}

	sb.WriteString("\n**Top baristas**\n")
	if len(drinkers) == 0 {
		sb.WriteString("_none yet_\n")
	}
	for _, u := range drinkers {
		fmt.Fprintf(&sb, "<@%s>: %d drinks\n", u.UserID, u.Count)
	}

	sb.WriteString("\n**Top refillers**\n")
	if len(refillers) == 0 {
		sb.WriteString("_none yet_\n")
	}
	for _, u := range refillers {
		fmt.Fprintf(&sb, "<@%s>: %d refills\n", u.UserID, u.Count)
	}

	sb.WriteString("\n**Top grounds-emptiers**\n")
	if len(emptiers) == 0 {
		sb.WriteString("_none yet_\n")
	}
	for _, e := range emptiers {
		fmt.Fprintf(&sb, "<@%s>: %d× · %dg total · %dg avg\n", e.UserID, e.Count, e.TotalGrams, avgGrams(e.TotalGrams, e.Count))
	}

	if len(slackers) > 0 {
		sb.WriteString("\n**Slackers** _(left it empty for the next person)_\n")
		for _, u := range slackers {
			fmt.Fprintf(&sb, "<@%s>: %d misses\n", u.UserID, u.Count)
		}
	}

	return strings.TrimRight(sb.String(), "\n")
}

// avgGrams returns the integer average of total over count, 0 when count is 0.
func avgGrams(total, count int) int {
	if count <= 0 {
		return 0
	}
	return int(math.Round(float64(total) / float64(count)))
}

// formatUserStats renders the detailed per-user breakdown for /coffeemachine
// stats: drinks by type, refills by part, grounds emptied, and slacker misses.
func formatUserStats(userID string, drinks, refills []labelCount, groundsCount, groundsTotal int, slackers []labelCount, penalties []pickupPenaltyStat, now time.Time) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "📊 **Coffee stats for <@%s>**\n", userID)

	sb.WriteString("\n**Drinks**\n")
	if len(drinks) == 0 {
		sb.WriteString("_none yet_\n")
	}
	for _, d := range drinks {
		fmt.Fprintf(&sb, "%s: %d\n", drinkKeyLabel(d.Key), d.Count)
	}

	sb.WriteString("\n**Refills**\n")
	if len(refills) == 0 {
		sb.WriteString("_none yet_\n")
	}
	for _, r := range refills {
		fmt.Fprintf(&sb, "%s: %d× (%d total)\n", titleCase(partLabel(r.Key)), r.Count, r.Amount)
	}

	if groundsCount > 0 {
		fmt.Fprintf(&sb, "\n**Grounds emptied:** %d× · %dg total · %dg avg\n", groundsCount, groundsTotal, avgGrams(groundsTotal, groundsCount))
	} else {
		sb.WriteString("\n**Grounds emptied:** never\n")
	}

	if len(slackers) > 0 {
		sb.WriteString("\n**Slacker misses** _(left empty for the next person)_\n")
		for _, s := range slackers {
			fmt.Fprintf(&sb, "%s: %d\n", titleCase(partLabel(s.Key)), s.Count)
		}
	}

	sb.WriteString("\n**Unclaimed-drink strikes** _(Discord-wide, last 90 days)_\n")
	if len(penalties) == 0 {
		sb.WriteString("_none active_\n")
	}
	for _, penalty := range penalties {
		fmt.Fprintf(&sb, "<@%s>: %d strikes", penalty.UserID, penalty.Strikes)
		switch {
		case now.Before(penalty.BlockedUntil):
			fmt.Fprintf(&sb, " · stage %d timeout until <t:%d:F> (<t:%d:R>)", penalty.Stage, penalty.BlockedUntil.Unix(), penalty.BlockedUntil.Unix())
		case now.Before(penalty.ProbationUntil):
			fmt.Fprintf(&sb, " · stage %d probation until <t:%d:F> (<t:%d:R>)", penalty.Stage, penalty.ProbationUntil.Unix(), penalty.ProbationUntil.Unix())
		}
		sb.WriteByte('\n')
	}

	return strings.TrimRight(sb.String(), "\n")
}

// drinkKeyLabel maps a drink key to its menu label and formats historical keys.
func drinkKeyLabel(key string) string {
	if r, ok := recipeByKey(key); ok {
		return r.label
	}
	return titleCase(strings.ReplaceAll(key, "_", " "))
}

// titleCase upper-cases the first rune of s.
func titleCase(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

// brewResponder abstracts how a brew flow talks back to Discord so the same
// dispense+animation logic serves both the slash commands (/coffee, /tea) and
// the interactive menus (component "go" button). Brewing, readiness, and blocks
// are all public so the channel can see who is at the machine.
type brewResponder struct {
	brewing func(content string)                                     // the initial "brewing…" status message
	final   func(content string, comps []discordgo.MessageComponent) // edit to the finished drink (carries the Take cup button)
	blocked func(content string)                                     // a brew that could not be served
}

// brewResponder builds the responder used by both the slash commands and the
// interactive menu. Both callers defer the interaction before calling executeBrew,
// so all messages here are edits. The final reveal carries the supplied components
// (the Take cup button); brewing and blocked messages drop components.
func (m *Module) brewResponder(s *discordgo.Session, i *discordgo.InteractionCreate) brewResponder {
	empty := []discordgo.MessageComponent{}
	return brewResponder{
		brewing: func(c string) { m.editWithComponents(s, i, c, empty) },
		blocked: func(c string) { m.editWithComponents(s, i, c, empty) },
		final:   func(c string, comps []discordgo.MessageComponent) { m.editWithComponents(s, i, c, comps) },
	}
}

// handleBrewInteraction serves /brew. With no options it opens the interactive
// drink menu (coffees + teas); otherwise it brews the chosen drink directly.
func (m *Module) handleBrewInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if m.rejectRestrictedBrew(s, i) {
		return
	}
	data := i.ApplicationCommandData()
	// Acknowledge immediately so neither menu translation nor brewing can miss
	// Discord's three-second interaction deadline.
	if err := m.deferInteraction(s, i, false); err != nil {
		slog.Error("coffee: defer brew failed", "error", err)
		return
	}
	if len(data.Options) == 0 {
		m.openBrewMenu(s, i)
		return
	}
	drinkKey := menu[0].key
	addMilk, addSugar := false, false
	for _, o := range data.Options {
		switch o.Name {
		case "drink":
			drinkKey = o.StringValue()
		case "milk":
			addMilk = o.BoolValue()
		case "sugar":
			addSugar = o.BoolValue()
		}
	}
	m.executeBrew(s, i, drinkKey, addMilk, addSugar, m.brewResponder(s, i))
}

func (m *Module) rejectRestrictedBrew(s *discordgo.Session, i *discordgo.InteractionCreate) bool {
	now := m.nowFunc().UTC()
	status, err := m.restrictionForUser(interactionUserID(i), now)
	if err != nil {
		slog.Error("coffee: restriction check failed", "error", err)
		return false
	}
	if !status.blocked(now) {
		return false
	}
	m.respond(s, i, formatRestriction(status.BlockedUntil, now), true)
	return true
}

// executeBrew dispenses one drink and drives the brewing animation through the
// supplied responder.
func (m *Module) executeBrew(s *discordgo.Session, i *discordgo.InteractionCreate, drinkKey string, addMilk, addSugar bool, r brewResponder) {
	out, err := m.dispense(i.GuildID, interactionUserID(i), drinkKey, addMilk, addSugar)
	if err != nil {
		slog.Error("coffee: dispense failed", "error", err)
		r.blocked(m.localizeUI(s, i.ChannelID, machineError))
		return
	}
	if !out.ok {
		// Blocked on a missing/low ingredient (or full grounds or tea bags). Keep
		// the exact fail message (with any blame) as the fallback so the
		// slash-command hint and user mention stay correct.
		fallback := blockedFallback(out)
		msg := m.generateInteractionMessage(s, i.ChannelID,
			"The coffee machine cannot make the drink right now: "+fallback+
				" Tell the user in one short sentence and keep the slash command hint and any user mention intact.",
			fallback)
		r.blocked(msg)
		return
	}

	label := drinkLabel(out.recipe)
	extras := extrasSuffix(out.splashMilk, out.withSugar)
	userID := interactionUserID(i)

	// Real machines take a few seconds; show a public brewing status with a
	// Discord relative-time countdown naming the orderer, then reveal the
	// finished drink. Wait varies by drink.
	wait := brewTime(out.recipe)
	readyAt := m.nowFunc().Add(wait)
	ts := fmt.Sprintf("<t:%d:R>", readyAt.Unix())
	brewing := m.generateInteractionMessage(s, i.ChannelID,
		fmt.Sprintf("User <@%s> ordered a %s%s. Tell the channel it is brewing for them now, in one short sentence, keeping the <@%s> mention.", userID, label, extras, userID),
		fmt.Sprintf("%s Brewing <@%s>'s %s%s…", drinkEmoji(out.recipe), userID, label, extras))
	r.brewing(brewing + " Ready " + ts)

	m.sleep(wait)

	// The reveal is public but personal: it names the orderer's own cup and
	// carries a Take cup button that only they may press to grab it.
	readyFallback := fmt.Sprintf("%s <@%s>, your %s%s is ready — grab it!", drinkEmoji(out.recipe), userID, label, extras)
	scenario := fmt.Sprintf("The %s%s ordered by user <@%s> is ready in the machine. Announce to the channel that it is waiting for them to grab, in one short sentence, keeping the <@%s> mention.", label, extras, userID, userID)
	// Fold the low-on-supplies nudge into the same generated message so it is
	// translated alongside the ready announcement (instead of being appended as
	// untranslated English); instruct the model to keep the exact command hints
	// so /coffeemachine stays clickable.
	hint := serviceHint(out.serviceNeeded)
	if hint != "" {
		scenario += " Also add a brief heads-up that the machine is running low: " + strings.TrimSpace(hint) + " Keep any `/coffeemachine` command and emoji exactly as written."
	}
	final := m.generateInteractionMessage(s, i.ChannelID, scenario, readyFallback+hint)
	order, err := m.markOrderReady(out.order.ID, m.nowFunc().UTC())
	if err != nil {
		slog.Error("coffee: failed to mark order ready", "error", err, "orderID", out.order.ID)
		r.blocked(m.localizeUI(s, i.ChannelID, machineError))
		return
	}
	r.final(final, takeCupComponents(order.ID))
}

// handleMachineInteraction handles /coffeemachine refill|empty|status.
func (m *Module) handleMachineInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ApplicationCommandData()
	if len(data.Options) == 0 {
		return
	}
	sub := data.Options[0]
	if err := m.deferInteraction(s, i, true); err != nil {
		slog.Error("coffee: defer machine interaction failed", "error", err)
		return
	}
	userID := interactionUserID(i)

	switch sub.Name {
	case "refill":
		partKey := ""
		for _, o := range sub.Options {
			if o.Name == "part" {
				partKey = o.StringValue()
			}
		}
		// Tea bag refills go to a separate store path; machine parts use refill().
		if flavor, ok := strings.CutPrefix(partKey, "tea_"); ok {
			out, err := m.refillTeaBags(i.GuildID, userID, flavor)
			if err != nil {
				slog.Error("coffee: tea bag refill failed", "error", err)
				m.finishMachineInteraction(s, i, m.localizeUI(s, i.ChannelID, machineError), true)
				return
			}
			if out.alreadyFull {
				msg := m.generateInteractionMessage(s, i.ChannelID,
					fmt.Sprintf("The %s tea bag box is already full. Tell the user in one short sentence.", out.label),
					fmt.Sprintf("%s tea bags are already full.", out.label))
				m.finishMachineInteraction(s, i, msg, true)
				return
			}
			msg := m.generateInteractionMessage(s, i.ChannelID,
				fmt.Sprintf("A user just restocked the %s tea bags to the top (added %d bags). Thank them in one short sentence.", out.label, out.added),
				fmt.Sprintf("🍵 <@%s> restocked %s tea bags (+%d bags).", userID, out.label, out.added))
			m.finishMachineInteraction(s, i, msg, false)
			return
		}
		out, err := m.refill(i.GuildID, userID, partKey)
		if err != nil {
			slog.Error("coffee: refill failed", "error", err)
			m.finishMachineInteraction(s, i, m.localizeUI(s, i.ChannelID, machineError), true)
			return
		}
		if out.alreadyFull {
			msg := m.generateInteractionMessage(s, i.ChannelID,
				fmt.Sprintf("The %s tank is already full. Tell the user in one short sentence.", out.part.label),
				fmt.Sprintf("%s is already full.", out.part.label))
			m.finishMachineInteraction(s, i, msg, true)
			return
		}
		msg := m.generateInteractionMessage(s, i.ChannelID,
			fmt.Sprintf("A user just refilled the %s to the top (added %d%s). Thank them in one short sentence.", out.part.label, out.added, out.part.unit),
			fmt.Sprintf("🛒 <@%s> refilled %s (+%d%s).", userID, out.part.label, out.added, out.part.unit))
		m.finishMachineInteraction(s, i, msg, false)

	case "empty":
		out, err := m.emptyGrounds(i.GuildID, userID)
		if err != nil {
			slog.Error("coffee: empty grounds failed", "error", err)
			m.finishMachineInteraction(s, i, m.localizeUI(s, i.ChannelID, machineError), true)
			return
		}
		if out.alreadyEmpty {
			msg := m.generateInteractionMessage(s, i.ChannelID,
				"The coffee grounds container is already empty. Tell the user in one short sentence.",
				"The grounds container is already empty.")
			m.finishMachineInteraction(s, i, msg, true)
			return
		}
		msg := m.generateInteractionMessage(s, i.ChannelID,
			fmt.Sprintf("A user just emptied the coffee grounds container (%dg removed). Thank them in one short sentence.", out.removed),
			fmt.Sprintf("🗑️ <@%s> emptied the grounds container (%dg removed).", userID, out.removed))
		m.finishMachineInteraction(s, i, msg, false)

	case "status":
		inv, err := m.getOrSeedInventory(i.GuildID)
		if err != nil {
			slog.Error("coffee: status failed", "error", err)
			m.finishMachineInteraction(s, i, m.localizeUI(s, i.ChannelID, machineError), true)
			return
		}
		drinkers, _ := m.topDrinkers(i.GuildID, 3)
		refillers, _ := m.topRefillers(i.GuildID, 3)
		emptiers, _ := m.topGroundsEmptiers(i.GuildID, 3)
		slackers, _ := m.topSlackers(i.GuildID, 3)
		teaBags, _ := m.getTeaBagInventory(i.GuildID)
		m.finishMachineInteraction(s, i, formatStatus(inv, drinkers, refillers, emptiers, slackers, teaBags), true)

	case "stats":
		targetID := userID
		for _, o := range sub.Options {
			if o.Name == "user" {
				if u := o.UserValue(s); u != nil {
					targetID = u.ID
				}
			}
		}
		m.finishMachineInteraction(s, i, m.buildUserStats(i.GuildID, targetID), true)
	}
}

func (m *Module) finishMachineInteraction(s *discordgo.Session, i *discordgo.InteractionCreate, content string, ephemeral bool) {
	if ephemeral {
		m.editDeferredResponse(s, i, content)
		return
	}
	if _, err := s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{Content: content}); err != nil {
		slog.Error("coffee: failed to send machine follow-up", "error", err)
		m.editDeferredResponse(s, i, content)
		return
	}
	if err := s.InteractionResponseDelete(i.Interaction); err != nil {
		slog.Error("coffee: failed to remove deferred machine response", "error", err)
	}
}

// buildUserStats gathers and renders the detailed per-user stat breakdown.
func (m *Module) buildUserStats(guildID, userID string) string {
	drinks, _ := m.userDrinkBreakdown(guildID, userID)
	refills, _ := m.userRefillBreakdown(guildID, userID)
	groundsCount, groundsTotal, _ := m.userGroundsStats(guildID, userID)
	slackers, _ := m.userSlackerBreakdown(guildID, userID)
	now := m.nowFunc().UTC()
	penalties, _ := m.pickupPenaltyStats(now)
	return formatUserStats(userID, drinks, refills, groundsCount, groundsTotal, slackers, penalties, now)
}

// --- Interactive order menu (no-options /brew) --------------------------------

// Component custom-ID prefixes. The menu is public, so every custom ID also
// carries the opener's user ID and only they may operate the components.
// brewCfgPrefix tags the unified brew menu; takeCupPrefix tags the Take cup
// button on a finished drink.
const (
	brewCfgPrefix = "coffee_brew_cfg"
	takeCupPrefix = "coffee_take"
)

const (
	brewMenuPrompt  = "☕🍵 What can I get you? Pick a drink, toggle the extras, then hit **Brew**."
	machineError    = "The machine sputtered and failed. Try again later."
	notYourOrderMsg = "☕ That's not your order — run `/brew` to start your own."
)

// brewCfg is the full state of an in-progress interactive order, carried inside
// every component custom ID so no server-side session state is needed. opener is
// the user who started the menu (only they may operate it); choice holds a full
// drinkKey from the menu (e.g. "coffee", "tea_black").
type brewCfg struct {
	opener string
	choice string
	milk   bool
	sugar  bool
}

func boolFlag(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// encodeBrewCfg renders an action plus the current order state into a component
// custom ID, e.g. "coffee_brew_cfg:milk:42:espresso:1:0".
func encodeBrewCfg(prefix, action string, c brewCfg) string {
	return strings.Join([]string{prefix, action, c.opener, c.choice, boolFlag(c.milk), boolFlag(c.sugar)}, ":")
}

// parseBrewCfg reverses encodeBrewCfg for the given prefix. Opener IDs and
// choice keys never contain a colon.
func parseBrewCfg(prefix, customID string) (action string, c brewCfg, ok bool) {
	parts := strings.Split(customID, ":")
	if len(parts) != 6 || parts[0] != prefix {
		return "", brewCfg{}, false
	}
	return parts[1], brewCfg{opener: parts[2], choice: parts[3], milk: parts[4] == "1", sugar: parts[5] == "1"}, true
}

// ensureOpener reports whether the clicking user owns this order; if not, it
// nudges them ephemerally and returns false.
func (m *Module) ensureOpener(s *discordgo.Session, i *discordgo.InteractionCreate, opener string) bool {
	if interactionUserID(i) == opener {
		return true
	}
	m.respond(s, i, m.localizeUI(s, i.ChannelID, notYourOrderMsg), true)
	return false
}

// extrasRow builds the milk/sugar toggle buttons and the Brew button for a menu.
func extrasRow(prefix string, c brewCfg) discordgo.ActionsRow {
	milkLabel, milkStyle := "🥛 Milk: off", discordgo.SecondaryButton
	if c.milk {
		milkLabel, milkStyle = "🥛 Milk: on", discordgo.SuccessButton
	}
	sugarLabel, sugarStyle := "🍬 Sugar: off", discordgo.SecondaryButton
	if c.sugar {
		sugarLabel, sugarStyle = "🍬 Sugar: on", discordgo.SuccessButton
	}
	return discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		discordgo.Button{Label: milkLabel, Style: milkStyle, CustomID: encodeBrewCfg(prefix, "milk", c)},
		discordgo.Button{Label: sugarLabel, Style: sugarStyle, CustomID: encodeBrewCfg(prefix, "sugar", c)},
		discordgo.Button{Label: "Brew", Emoji: &discordgo.ComponentEmoji{Name: "☕"}, Style: discordgo.PrimaryButton, CustomID: encodeBrewCfg(prefix, "go", c)},
	}}
}

// brewMenuComponents builds the unified /brew drink select (coffees + teas)
// plus the extras row.
func brewMenuComponents(c brewCfg) []discordgo.MessageComponent {
	options := make([]discordgo.SelectMenuOption, 0, len(menu))
	for _, r := range menu {
		emoji := "☕"
		if strings.HasPrefix(r.key, "tea_") {
			emoji = "🍵"
		}
		options = append(options, discordgo.SelectMenuOption{
			Label:   r.label,
			Value:   r.key,
			Default: r.key == c.choice,
			Emoji:   &discordgo.ComponentEmoji{Name: emoji},
		})
	}
	return []discordgo.MessageComponent{
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.SelectMenu{CustomID: encodeBrewCfg(brewCfgPrefix, "pick", c), Placeholder: "Choose your drink", Options: options},
		}},
		extrasRow(brewCfgPrefix, c),
	}
}

// takeCupComponents builds the single-button row offering to grab a finished
// drink out of the machine. The custom ID carries the orderer (only they may
// take it) and the drink key so the confirmation can name it.
func takeCupComponents(orderID uint) []discordgo.MessageComponent {
	id := fmt.Sprintf("%s:%d", takeCupPrefix, orderID)
	return []discordgo.MessageComponent{
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{Label: "Take cup", Emoji: &discordgo.ComponentEmoji{Name: "🫴"}, Style: discordgo.SuccessButton, CustomID: id},
		}},
	}
}

// openBrewMenu shows the interactive unified brew menu, gated to its opener.
func (m *Module) openBrewMenu(s *discordgo.Session, i *discordgo.InteractionCreate) {
	c := brewCfg{opener: interactionUserID(i), choice: menu[0].key}
	prompt := m.localizeUI(s, i.ChannelID, brewMenuPrompt)
	_ = m.localizeUI(s, i.ChannelID, machineError)
	_ = m.localizeUI(s, i.ChannelID, notYourOrderMsg)
	m.openMenu(s, i, prompt, brewMenuComponents(c))
}

// handleBrewComponent processes clicks on the interactive brew menu: drink
// selection and toggles re-render in place; Brew dispenses the configured drink.
func (m *Module) handleBrewComponent(s *discordgo.Session, i *discordgo.InteractionCreate) {
	action, c, ok := parseBrewCfg(brewCfgPrefix, i.MessageComponentData().CustomID)
	if !ok || !m.ensureOpener(s, i, c.opener) {
		return
	}
	if action == "go" {
		if m.rejectRestrictedBrew(s, i) {
			return
		}
		if err := m.deferUpdate(s, i); err != nil {
			slog.Error("coffee: defer brew component failed", "error", err)
			return
		}
		m.executeBrew(s, i, c.choice, c.milk, c.sugar, m.brewResponder(s, i))
		return
	}
	prompt := m.localizeUI(s, i.ChannelID, brewMenuPrompt)
	switch action {
	case "pick":
		if vals := i.MessageComponentData().Values; len(vals) > 0 {
			c.choice = vals[0]
		}
		m.updateMenu(s, i, prompt, brewMenuComponents(c))
	case "milk":
		c.milk = !c.milk
		m.updateMenu(s, i, prompt, brewMenuComponents(c))
	case "sugar":
		c.sugar = !c.sugar
		m.updateMenu(s, i, prompt, brewMenuComponents(c))
	}
}

// handleTakeCupComponent acknowledges the Take cup button: only the orderer may
// take it. It edits the public drink message into a personal "grabbed it"
// confirmation and drops the button.
func (m *Module) handleTakeCupComponent(s *discordgo.Session, i *discordgo.InteractionCreate) {
	parts := strings.SplitN(i.MessageComponentData().CustomID, ":", 2)
	if len(parts) != 2 {
		m.respond(s, i, "This order is no longer tracked. Please use `/brew` again.", true)
		return
	}
	orderID, err := strconv.ParseUint(parts[1], 10, 64)
	if err != nil {
		m.respond(s, i, "This order is no longer tracked. Please use `/brew` again.", true)
		return
	}
	userID := interactionUserID(i)
	var order DrinkOrder
	if err = m.getDB().First(&order, uint(orderID)).Error; err != nil {
		m.respond(s, i, "This order is no longer tracked. Please use `/brew` again.", true)
		return
	}
	if order.UserID != userID {
		m.respond(s, i, m.localizeUI(s, i.ChannelID, notYourOrderMsg), true)
		return
	}
	if err = m.deferUpdate(s, i); err != nil {
		slog.Error("coffee: defer take-cup failed", "error", err)
		return
	}
	result, err := m.pickupOrder(uint(orderID), userID, m.nowFunc().UTC())
	if err != nil {
		slog.Error("coffee: take-cup failed", "error", err, "orderID", orderID)
		m.respond(s, i, machineError, true)
		return
	}
	label := "drink"
	if r, ok := recipeByKey(result.order.Drink); ok {
		label = drinkLabel(r)
	}
	if result.expired {
		m.editWithComponents(s, i, "This drink expired because it was not picked up within 20 minutes.", []discordgo.MessageComponent{})
		return
	}
	if !result.picked {
		m.editWithComponents(s, i, "This drink is no longer waiting in the machine.", []discordgo.MessageComponent{})
		return
	}
	msg := m.generateInteractionMessage(s, i.ChannelID,
		fmt.Sprintf("User <@%s> just grabbed their %s out of the coffee machine. Tell the channel to enjoy it, in one short sentence, keeping the <@%s> mention.", userID, label, userID),
		fmt.Sprintf("%s <@%s> grabbed their %s out of the machine. Enjoy!", drinkEmojiForKey(result.order.Drink), userID, label))
	m.editWithComponents(s, i, msg, []discordgo.MessageComponent{})
}

// drinkEmojiForKey is drinkEmoji by key, falling back to a coffee cup.
func drinkEmojiForKey(drinkKey string) string {
	if r, ok := recipeByKey(drinkKey); ok {
		return drinkEmoji(r)
	}
	return "☕"
}
