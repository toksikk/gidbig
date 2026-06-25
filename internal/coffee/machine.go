package coffee

import (
	"errors"
	"fmt"
	"log/slog"
	"math"
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

// menu is the ordered drink list. The first entry is the default for /coffee;
// hot_water is served only via /tea.
var menu = []recipe{
	{key: "coffee", label: "Coffee", bean: beanMild, beanGrams: 11, waterMl: 120, groundsG: 20, allowsMilk: true, brewSecs: 28},
	{key: "espresso", label: "Espresso", bean: beanEspresso, beanGrams: 9, waterMl: 40, groundsG: 18, allowsMilk: true, brewSecs: 24},
	{key: "milk_coffee", label: "Milk coffee", bean: beanMild, beanGrams: 11, waterMl: 80, milkMl: 120, groundsG: 20, brewSecs: 32},
	{key: "latte_macchiato", label: "Latte macchiato", bean: beanEspresso, beanGrams: 9, waterMl: 40, milkMl: 180, groundsG: 18, brewSecs: 36},
	{key: "flat_white", label: "Flat white", bean: beanEspresso, beanGrams: 18, waterMl: 60, milkMl: 120, groundsG: 36, brewSecs: 40},
	{key: "hot_water", label: "Hot water", bean: beanNone, waterMl: 200, allowsMilk: true, brewSecs: 20},
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

// teaFlavor is an optional tea-bag flavor for the hot-water drink. Tea is
// cosmetic: it flavors the cup but consumes no tracked inventory.
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
}

// teaLabel returns the display label for a tea-flavor key.
func teaLabel(key string) (string, bool) {
	for _, t := range teaFlavors {
		if t.key == key {
			return t.label, true
		}
	}
	return "", false
}

// refillPart describes a refillable tank/hopper exposed by /coffeemachine refill.
type refillPart struct {
	key   string // choice value, also RefillEvent.Part
	label string
	max   int
	unit  string // "g" or "ml"
}

var refillParts = []refillPart{
	{key: "beans_mild", label: "Mild beans", max: maxBeansMildG, unit: "g"},
	{key: "beans_espresso", label: "Espresso beans", max: maxBeansEspressoG, unit: "g"},
	{key: "water", label: "Water", max: maxWaterMl, unit: "ml"},
	{key: "milk", label: "Milk", max: maxMilkMl, unit: "ml"},
}

func refillPartByKey(key string) (refillPart, bool) {
	for _, p := range refillParts {
		if p.key == key {
			return p, true
		}
	}
	return refillPart{}, false
}

// partLabel returns a human-facing name for a refillable part or the grounds
// container.
func partLabel(key string) string {
	if key == partGrounds {
		return "grounds container"
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
	recipe     recipe
	inventory  MachineInventory
	ok         bool
	failMsg    string // user-facing reason when ok is false
	splashMilk bool   // an optional milk splash was added to a black drink
	withSugar  bool

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

	m.machineMu.Lock()
	defer m.machineMu.Unlock()

	err := d.Transaction(func(tx *gorm.DB) error {
		inv, e := seedInventoryTx(tx, guildID)
		if e != nil {
			return e
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
		if e = tx.Create(&DrinkEvent{
			GuildID:   guildID,
			UserID:    userID,
			Drink:     r.key,
			WithMilk:  withMilk,
			WithSugar: addSugar,
		}).Error; e != nil {
			return e
		}
		// Record which parts this brew left needing service and pin the brewer as
		// responsible, so a later blocked brew can blame them.
		out.serviceNeeded = partsNeedingService(inv)
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

// drinkLabel is the display name for a served drink. A non-empty tea flavor
// turns hot water into the named tea (ignored for any other drink).
func drinkLabel(r recipe, tea string) string {
	if r.key == "hot_water" && tea != "" {
		if tl, ok := teaLabel(tea); ok {
			return tl + " tea"
		}
		return "tea"
	}
	return r.label
}

// drinkEmoji picks the cup emoji for a served drink.
func drinkEmoji(r recipe, tea string) string {
	if r.key == "hot_water" && tea != "" {
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
func formatDispenseSuccess(r recipe, splashMilk, withSugar bool, tea string) string {
	return fmt.Sprintf("%s Here's your %s%s!", drinkEmoji(r, tea), drinkLabel(r, tea), extrasSuffix(splashMilk, withSugar))
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
func formatStatus(inv MachineInventory, drinkers, refillers []userCount, emptiers []groundsEmptier, slackers []userCount) string {
	var sb strings.Builder
	sb.WriteString("☕ **Coffee machine status**\n")
	fmt.Fprintf(&sb, "Mild beans: %d/%dg (%d%%)\n", inv.BeansMildGrams, maxBeansMildG, percent(inv.BeansMildGrams, maxBeansMildG))
	fmt.Fprintf(&sb, "Espresso beans: %d/%dg (%d%%)\n", inv.BeansEspressoGrams, maxBeansEspressoG, percent(inv.BeansEspressoGrams, maxBeansEspressoG))
	fmt.Fprintf(&sb, "Water: %d/%dml (%d%%)\n", inv.WaterMl, maxWaterMl, percent(inv.WaterMl, maxWaterMl))
	fmt.Fprintf(&sb, "Milk: %d/%dml (%d%%)\n", inv.MilkMl, maxMilkMl, percent(inv.MilkMl, maxMilkMl))
	fmt.Fprintf(&sb, "Grounds: %d/%dg (%d%%)\n", inv.GroundsGrams, maxGroundsG, percent(inv.GroundsGrams, maxGroundsG))

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
func formatUserStats(userID string, drinks, refills []labelCount, groundsCount, groundsTotal int, slackers []labelCount) string {
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

	return strings.TrimRight(sb.String(), "\n")
}

// drinkKeyLabel maps a drink key to its menu label, falling back to the key.
func drinkKeyLabel(key string) string {
	if r, ok := recipeByKey(key); ok {
		return r.label
	}
	return key
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
// the interactive menus (component "go" button). Every brew is private to the
// invoker.
type brewResponder struct {
	brewing func(content string)                                     // the initial "brewing…" status message
	final   func(content string, comps []discordgo.MessageComponent) // edit to the finished drink (carries the Take cup button)
	blocked func(content string)                                     // a brew that could not be served
}

// brewResponder builds the responder used by both the slash commands and the
// interactive menu. Every brew is private to the invoker: the slash path replies
// ephemerally and edits that reply; the menu path updates its ephemeral message
// in place. The final reveal carries the supplied components (the Take cup
// button); brewing/blocked drop components.
func (m *Module) brewResponder(s *discordgo.Session, i *discordgo.InteractionCreate, fromMenu bool) brewResponder {
	r := brewResponder{
		final: func(c string, comps []discordgo.MessageComponent) { m.editWithComponents(s, i, c, comps) },
	}
	if fromMenu {
		r.brewing = func(c string) { m.respondUpdate(s, i, c) }
		r.blocked = func(c string) { m.respondUpdate(s, i, c) }
	} else {
		r.brewing = func(c string) { m.respond(s, i, c, true) }
		r.blocked = func(c string) { m.respond(s, i, c, true) }
	}
	return r
}

// handleCoffeeInteraction serves /coffee. With no options it opens the
// interactive drink menu; otherwise it brews the chosen coffee directly.
func (m *Module) handleCoffeeInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ApplicationCommandData()
	if len(data.Options) == 0 {
		m.openCoffeeMenu(s, i)
		return
	}
	drinkKey := coffeeMenu()[0].key
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
	m.executeBrew(s, i, drinkKey, addMilk, addSugar, "", m.brewResponder(s, i, false))
}

// handleTeaInteraction serves /tea: hot water with a chosen tea-bag flavor and
// optional milk/sugar. With no options it opens the interactive tea menu.
func (m *Module) handleTeaInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ApplicationCommandData()
	if len(data.Options) == 0 {
		m.openTeaMenu(s, i)
		return
	}
	tea := teaFlavors[0].key
	addMilk, addSugar := false, false
	for _, o := range data.Options {
		switch o.Name {
		case "flavor":
			tea = o.StringValue()
		case "milk":
			addMilk = o.BoolValue()
		case "sugar":
			addSugar = o.BoolValue()
		}
	}
	m.executeBrew(s, i, "hot_water", addMilk, addSugar, tea, m.brewResponder(s, i, false))
}

// executeBrew dispenses one drink and drives the brewing animation through the
// supplied responder. tea is cosmetic and only applies to hot water.
func (m *Module) executeBrew(s *discordgo.Session, i *discordgo.InteractionCreate, drinkKey string, addMilk, addSugar bool, tea string, r brewResponder) {
	out, err := m.dispense(i.GuildID, interactionUserID(i), drinkKey, addMilk, addSugar)
	if err != nil {
		slog.Error("coffee: dispense failed", "error", err)
		r.blocked("The machine sputtered and failed. Try again later.")
		return
	}
	if !out.ok {
		// Blocked on a missing/low ingredient (or full grounds). Keep the exact
		// fail message (with any blame) as the fallback so the slash-command hint
		// and user mention stay correct.
		fallback := blockedFallback(out)
		msg := m.generateInteractionMessage(s, i.ChannelID,
			"The coffee machine cannot make the drink right now: "+fallback+
				" Tell the user in one short sentence and keep the slash command hint and any user mention intact.",
			fallback)
		r.blocked(msg)
		return
	}

	label := drinkLabel(out.recipe, tea)
	extras := extrasSuffix(out.splashMilk, out.withSugar)

	// Real machines take a few seconds; show a brewing status with a Discord
	// relative-time countdown, then reveal the finished drink. Wait varies by drink.
	wait := brewTime(out.recipe)
	readyAt := m.nowFunc().Add(wait)
	ts := fmt.Sprintf("<t:%d:R>", readyAt.Unix())
	brewing := m.generateInteractionMessage(s, i.ChannelID,
		fmt.Sprintf("A user ordered a %s%s. Tell them it is brewing now, in one short sentence.", label, extras),
		fmt.Sprintf("%s Brewing your %s%s…", drinkEmoji(out.recipe, tea), label, extras))
	r.brewing(brewing + " Ready " + ts)

	m.sleep(wait)

	// The reply is private to the orderer, so the reveal is personal — their cup,
	// for them to take — with a button to grab it out of the machine.
	final := m.generateInteractionMessage(s, i.ChannelID,
		fmt.Sprintf("The user's own %s%s is ready in the machine. Tell them personally that their cup is ready to grab, in one short sentence.", label, extras),
		formatDispenseSuccess(out.recipe, out.splashMilk, out.withSugar, tea))
	// Append the service nudge verbatim (never paraphrased by the LLM) so the
	// brewer who left the machine low is reminded to refill for the next person.
	final += serviceHint(out.serviceNeeded)
	r.final(final, takeCupComponents(out.recipe.key, tea))
}

// handleMachineInteraction handles /coffeemachine refill|empty|status.
func (m *Module) handleMachineInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ApplicationCommandData()
	if len(data.Options) == 0 {
		return
	}
	sub := data.Options[0]
	userID := interactionUserID(i)

	switch sub.Name {
	case "refill":
		partKey := ""
		for _, o := range sub.Options {
			if o.Name == "part" {
				partKey = o.StringValue()
			}
		}
		out, err := m.refill(i.GuildID, userID, partKey)
		if err != nil {
			slog.Error("coffee: refill failed", "error", err)
			m.respond(s, i, "The machine sputtered and failed. Try again later.", true)
			return
		}
		if out.alreadyFull {
			msg := m.generateInteractionMessage(s, i.ChannelID,
				fmt.Sprintf("The %s tank is already full. Tell the user in one short sentence.", out.part.label),
				fmt.Sprintf("%s is already full.", out.part.label))
			m.respond(s, i, msg, true)
			return
		}
		msg := m.generateInteractionMessage(s, i.ChannelID,
			fmt.Sprintf("A user just refilled the %s to the top (added %d%s). Thank them in one short sentence.", out.part.label, out.added, out.part.unit),
			fmt.Sprintf("🛒 <@%s> refilled %s (+%d%s).", userID, out.part.label, out.added, out.part.unit))
		m.respond(s, i, msg, false)

	case "empty":
		out, err := m.emptyGrounds(i.GuildID, userID)
		if err != nil {
			slog.Error("coffee: empty grounds failed", "error", err)
			m.respond(s, i, "The machine sputtered and failed. Try again later.", true)
			return
		}
		if out.alreadyEmpty {
			msg := m.generateInteractionMessage(s, i.ChannelID,
				"The coffee grounds container is already empty. Tell the user in one short sentence.",
				"The grounds container is already empty.")
			m.respond(s, i, msg, true)
			return
		}
		msg := m.generateInteractionMessage(s, i.ChannelID,
			fmt.Sprintf("A user just emptied the coffee grounds container (%dg removed). Thank them in one short sentence.", out.removed),
			fmt.Sprintf("🗑️ <@%s> emptied the grounds container (%dg removed).", userID, out.removed))
		m.respond(s, i, msg, false)

	case "status":
		inv, err := m.getOrSeedInventory(i.GuildID)
		if err != nil {
			slog.Error("coffee: status failed", "error", err)
			m.respond(s, i, "The machine sputtered and failed. Try again later.", true)
			return
		}
		drinkers, _ := m.topDrinkers(i.GuildID, 3)
		refillers, _ := m.topRefillers(i.GuildID, 3)
		emptiers, _ := m.topGroundsEmptiers(i.GuildID, 3)
		slackers, _ := m.topSlackers(i.GuildID, 3)
		m.respond(s, i, formatStatus(inv, drinkers, refillers, emptiers, slackers), true)

	case "stats":
		targetID := userID
		for _, o := range sub.Options {
			if o.Name == "user" {
				if u := o.UserValue(s); u != nil {
					targetID = u.ID
				}
			}
		}
		m.respond(s, i, m.buildUserStats(i.GuildID, targetID), true)
	}
}

// buildUserStats gathers and renders the detailed per-user stat breakdown.
func (m *Module) buildUserStats(guildID, userID string) string {
	drinks, _ := m.userDrinkBreakdown(guildID, userID)
	refills, _ := m.userRefillBreakdown(guildID, userID)
	groundsCount, groundsTotal, _ := m.userGroundsStats(guildID, userID)
	slackers, _ := m.userSlackerBreakdown(guildID, userID)
	return formatUserStats(userID, drinks, refills, groundsCount, groundsTotal, slackers)
}

// --- Interactive order menus (no-options /coffee and /tea) -------------------

// Component custom-ID prefixes. The menus are ephemeral, so only the invoker
// can see and operate their components. coffeeCfgPrefix and teaCfgPrefix tag the
// two order menus; takeCupPrefix tags the Take cup button on a finished drink.
const (
	coffeeCfgPrefix = "coffee_brew_cfg"
	teaCfgPrefix    = "coffee_tea_cfg"
	takeCupPrefix   = "coffee_take"
)

const (
	coffeeMenuPrompt = "☕ What can I get you? Pick a coffee, toggle the extras, then hit **Brew**."
	teaMenuPrompt    = "🍵 Fancy a tea? Pick a flavor, toggle the extras, then hit **Brew**."
)

// coffeeMenu returns the menu drinks offered by /coffee — everything but hot
// water, which now lives behind /tea.
func coffeeMenu() []recipe {
	out := make([]recipe, 0, len(menu))
	for _, r := range menu {
		if r.key == "hot_water" {
			continue
		}
		out = append(out, r)
	}
	return out
}

// brewCfg is the full state of an in-progress interactive order, carried inside
// every component custom ID so no server-side session state is needed. choice
// holds a coffee drink key (coffee menu) or a tea flavor key (tea menu).
type brewCfg struct {
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
// custom ID, e.g. "coffee_brew_cfg:milk:espresso:1:0".
func encodeBrewCfg(prefix, action string, c brewCfg) string {
	return strings.Join([]string{prefix, action, c.choice, boolFlag(c.milk), boolFlag(c.sugar)}, ":")
}

// parseBrewCfg reverses encodeBrewCfg for the given prefix. Choice keys never
// contain a colon.
func parseBrewCfg(prefix, customID string) (action string, c brewCfg, ok bool) {
	parts := strings.Split(customID, ":")
	if len(parts) != 5 || parts[0] != prefix {
		return "", brewCfg{}, false
	}
	return parts[1], brewCfg{choice: parts[2], milk: parts[3] == "1", sugar: parts[4] == "1"}, true
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

// coffeeMenuComponents builds the /coffee drink select plus the extras row.
func coffeeMenuComponents(c brewCfg) []discordgo.MessageComponent {
	coffees := coffeeMenu()
	options := make([]discordgo.SelectMenuOption, 0, len(coffees))
	for _, r := range coffees {
		options = append(options, discordgo.SelectMenuOption{Label: r.label, Value: r.key, Default: r.key == c.choice})
	}
	return []discordgo.MessageComponent{
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.SelectMenu{CustomID: encodeBrewCfg(coffeeCfgPrefix, "pick", c), Placeholder: "Choose your coffee", Options: options},
		}},
		extrasRow(coffeeCfgPrefix, c),
	}
}

// teaMenuComponents builds the /tea flavor select plus the extras row.
func teaMenuComponents(c brewCfg) []discordgo.MessageComponent {
	options := make([]discordgo.SelectMenuOption, 0, len(teaFlavors))
	for _, t := range teaFlavors {
		options = append(options, discordgo.SelectMenuOption{Label: t.label, Value: t.key, Default: t.key == c.choice})
	}
	return []discordgo.MessageComponent{
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.SelectMenu{CustomID: encodeBrewCfg(teaCfgPrefix, "pick", c), Placeholder: "Choose your tea", Options: options},
		}},
		extrasRow(teaCfgPrefix, c),
	}
}

// takeCupComponents builds the single-button row offering to grab a finished
// drink out of the machine, encoding the drink so the confirmation can name it.
func takeCupComponents(drinkKey, tea string) []discordgo.MessageComponent {
	id := strings.Join([]string{takeCupPrefix, drinkKey, tea}, ":")
	return []discordgo.MessageComponent{
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{Label: "Take cup", Emoji: &discordgo.ComponentEmoji{Name: "🫴"}, Style: discordgo.SuccessButton, CustomID: id},
		}},
	}
}

// openCoffeeMenu shows the interactive coffee menu as an ephemeral message.
func (m *Module) openCoffeeMenu(s *discordgo.Session, i *discordgo.InteractionCreate) {
	c := brewCfg{choice: coffeeMenu()[0].key}
	m.openMenu(s, i, coffeeMenuPrompt, coffeeMenuComponents(c))
}

// openTeaMenu shows the interactive tea menu as an ephemeral message.
func (m *Module) openTeaMenu(s *discordgo.Session, i *discordgo.InteractionCreate) {
	c := brewCfg{choice: teaFlavors[0].key}
	m.openMenu(s, i, teaMenuPrompt, teaMenuComponents(c))
}

// handleCoffeeComponent processes clicks on the interactive coffee menu: drink
// selection and toggles re-render in place; Brew dispenses the configured drink.
func (m *Module) handleCoffeeComponent(s *discordgo.Session, i *discordgo.InteractionCreate) {
	action, c, ok := parseBrewCfg(coffeeCfgPrefix, i.MessageComponentData().CustomID)
	if !ok {
		return
	}
	switch action {
	case "pick":
		if vals := i.MessageComponentData().Values; len(vals) > 0 {
			c.choice = vals[0]
		}
		m.updateMenu(s, i, coffeeMenuPrompt, coffeeMenuComponents(c))
	case "milk":
		c.milk = !c.milk
		m.updateMenu(s, i, coffeeMenuPrompt, coffeeMenuComponents(c))
	case "sugar":
		c.sugar = !c.sugar
		m.updateMenu(s, i, coffeeMenuPrompt, coffeeMenuComponents(c))
	case "go":
		m.executeBrew(s, i, c.choice, c.milk, c.sugar, "", m.brewResponder(s, i, true))
	}
}

// handleTeaComponent processes clicks on the interactive tea menu: flavor
// selection and toggles re-render in place; Brew steeps the configured tea.
func (m *Module) handleTeaComponent(s *discordgo.Session, i *discordgo.InteractionCreate) {
	action, c, ok := parseBrewCfg(teaCfgPrefix, i.MessageComponentData().CustomID)
	if !ok {
		return
	}
	switch action {
	case "pick":
		if vals := i.MessageComponentData().Values; len(vals) > 0 {
			c.choice = vals[0]
		}
		m.updateMenu(s, i, teaMenuPrompt, teaMenuComponents(c))
	case "milk":
		c.milk = !c.milk
		m.updateMenu(s, i, teaMenuPrompt, teaMenuComponents(c))
	case "sugar":
		c.sugar = !c.sugar
		m.updateMenu(s, i, teaMenuPrompt, teaMenuComponents(c))
	case "go":
		m.executeBrew(s, i, "hot_water", c.milk, c.sugar, c.choice, m.brewResponder(s, i, true))
	}
}

// handleTakeCupComponent acknowledges the Take cup button: it edits the private
// drink message into a personal "grabbed it" confirmation and drops the button.
func (m *Module) handleTakeCupComponent(s *discordgo.Session, i *discordgo.InteractionCreate) {
	parts := strings.SplitN(i.MessageComponentData().CustomID, ":", 3)
	drinkKey, tea := "", ""
	if len(parts) >= 2 {
		drinkKey = parts[1]
	}
	if len(parts) >= 3 {
		tea = parts[2]
	}
	label := "drink"
	if r, ok := recipeByKey(drinkKey); ok {
		label = drinkLabel(r, tea)
	}
	m.respondUpdate(s, i, fmt.Sprintf("%s You grabbed your %s out of the machine. Enjoy!", drinkEmojiForKey(drinkKey, tea), label))
}

// drinkEmojiForKey is drinkEmoji by key, falling back to a coffee cup.
func drinkEmojiForKey(drinkKey, tea string) string {
	if r, ok := recipeByKey(drinkKey); ok {
		return drinkEmoji(r, tea)
	}
	return "☕"
}
