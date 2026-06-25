package coffee

import (
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"

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
}

// menu is the ordered drink list. The first entry is the default for /brew.
var menu = []recipe{
	{key: "coffee", label: "Coffee", bean: beanMild, beanGrams: 11, waterMl: 120, groundsG: 20, allowsMilk: true},
	{key: "espresso", label: "Espresso", bean: beanEspresso, beanGrams: 9, waterMl: 40, groundsG: 18, allowsMilk: true},
	{key: "milk_coffee", label: "Milk coffee", bean: beanMild, beanGrams: 11, waterMl: 80, milkMl: 120, groundsG: 20},
	{key: "latte_macchiato", label: "Latte macchiato", bean: beanEspresso, beanGrams: 9, waterMl: 40, milkMl: 180, groundsG: 18},
	{key: "flat_white", label: "Flat white", bean: beanEspresso, beanGrams: 18, waterMl: 60, milkMl: 120, groundsG: 36},
	{key: "hot_water", label: "Hot water", bean: beanNone, waterMl: 200, allowsMilk: true},
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

		switch {
		case r.bean == beanMild && inv.BeansMildGrams < r.beanGrams:
			out.failMsg = outOfMsg("mild beans", "beans_mild")
		case r.bean == beanEspresso && inv.BeansEspressoGrams < r.beanGrams:
			out.failMsg = outOfMsg("espresso beans", "beans_espresso")
		case inv.WaterMl < r.waterMl:
			out.failMsg = outOfMsg("water", "water")
		case inv.MilkMl < milkNeeded:
			out.failMsg = outOfMsg("milk", "milk")
		case inv.GroundsGrams+r.groundsG > maxGroundsG:
			out.failMsg = "The grounds container is full. Empty it with `/coffeemachine empty`."
		}
		if out.failMsg != "" {
			out.inventory = inv
			return nil // commit nothing; caller sees ok=false
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

// levelsLine renders all consumable levels on a single line.
func (inv MachineInventory) levelsLine() string {
	return fmt.Sprintf("Mild beans %d/%dg · Espresso beans %d/%dg · Water %d/%dml · Milk %d/%dml · Grounds %d/%dg",
		inv.BeansMildGrams, maxBeansMildG, inv.BeansEspressoGrams, maxBeansEspressoG,
		inv.WaterMl, maxWaterMl, inv.MilkMl, maxMilkMl, inv.GroundsGrams, maxGroundsG)
}

// formatDispenseSuccess builds the public confirmation for a served drink. A
// non-empty tea flavor turns hot water into the named tea (ignored otherwise).
func formatDispenseSuccess(r recipe, splashMilk, withSugar bool, tea string, inv MachineInventory) string {
	label, emoji := r.label, "☕"
	if r.key == "hot_water" && tea != "" {
		emoji = "🍵"
		if tl, ok := teaLabel(tea); ok {
			label = tl + " tea"
		} else {
			label = "tea"
		}
	}
	extras := []string{}
	if splashMilk {
		extras = append(extras, "milk")
	}
	if withSugar {
		extras = append(extras, "sugar")
	}
	suffix := ""
	if len(extras) > 0 {
		suffix = " with " + strings.Join(extras, " and ")
	}
	return fmt.Sprintf("%s Here's your %s%s!\n%s", emoji, label, suffix, inv.levelsLine())
}

// formatStatus renders the machine status, levels, and stat leaderboards.
func formatStatus(inv MachineInventory, drinkers, refillers []userCount, groundsEmpties int64) string {
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

	fmt.Fprintf(&sb, "\nGrounds emptied %d times.", groundsEmpties)
	return sb.String()
}

// handleBrewInteraction serves a drink for /brew.
func (m *Module) handleBrewInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ApplicationCommandData()
	drinkKey := menu[0].key
	addMilk, addSugar := false, false
	tea := ""
	for _, o := range data.Options {
		switch o.Name {
		case "drink":
			drinkKey = o.StringValue()
		case "milk":
			addMilk = o.BoolValue()
		case "sugar":
			addSugar = o.BoolValue()
		case "tea":
			tea = o.StringValue()
		}
	}

	out, err := m.dispense(i.GuildID, interactionUserID(i), drinkKey, addMilk, addSugar)
	if err != nil {
		slog.Error("coffee: dispense failed", "error", err)
		m.respond(s, i, "The machine sputtered and failed. Try again later.", true)
		return
	}
	if !out.ok {
		m.respond(s, i, out.failMsg, true)
		return
	}
	m.respond(s, i, formatDispenseSuccess(out.recipe, out.splashMilk, out.withSugar, tea, out.inventory), false)
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
			m.respond(s, i, fmt.Sprintf("%s is already full.", out.part.label), true)
			return
		}
		m.respond(s, i, fmt.Sprintf("🛒 <@%s> refilled %s (+%d%s).\n%s",
			userID, out.part.label, out.added, out.part.unit, out.inventory.levelsLine()), false)

	case "empty":
		out, err := m.emptyGrounds(i.GuildID, userID)
		if err != nil {
			slog.Error("coffee: empty grounds failed", "error", err)
			m.respond(s, i, "The machine sputtered and failed. Try again later.", true)
			return
		}
		if out.alreadyEmpty {
			m.respond(s, i, "The grounds container is already empty.", true)
			return
		}
		m.respond(s, i, fmt.Sprintf("🗑️ <@%s> emptied the grounds container (%dg removed).", userID, out.removed), false)

	case "status":
		inv, err := m.getOrSeedInventory(i.GuildID)
		if err != nil {
			slog.Error("coffee: status failed", "error", err)
			m.respond(s, i, "The machine sputtered and failed. Try again later.", true)
			return
		}
		drinkers, _ := m.topDrinkers(i.GuildID, 3)
		refillers, _ := m.topRefillers(i.GuildID, 3)
		groundsEmpties, _ := m.groundsEmptiedCount(i.GuildID)
		m.respond(s, i, formatStatus(inv, drinkers, refillers, groundsEmpties), true)
	}
}
