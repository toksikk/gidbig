package coffee

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/toksikk/gidbig/internal/bot"
)

// Stats reports coffee counters for /status.
func (m *Module) Stats(ctx context.Context) (bot.ModuleStats, error) {
	db := m.getDB()
	if db == nil {
		return bot.ModuleStats{}, errors.New("store not open")
	}
	db = db.WithContext(ctx)

	var drinks, drinkers, refills, openOrders, machines, violations, blocked int64
	queries := []struct {
		dst *int64
		run func(*int64) error
	}{
		{&drinks, func(n *int64) error { return db.Model(&DrinkEvent{}).Count(n).Error }},
		{&drinkers, func(n *int64) error { return db.Model(&DrinkEvent{}).Distinct("user_id").Count(n).Error }},
		{&refills, func(n *int64) error { return db.Model(&RefillEvent{}).Count(n).Error }},
		{&openOrders, func(n *int64) error {
			return db.Model(&DrinkOrder{}).Where("status IN ?", []string{orderStatusBrewing, orderStatusReady}).Count(n).Error
		}},
		{&machines, func(n *int64) error { return db.Model(&MachineInventory{}).Count(n).Error }},
		{&violations, func(n *int64) error { return db.Model(&PickupViolation{}).Count(n).Error }},
		{&blocked, func(n *int64) error {
			return db.Model(&BrewRestriction{}).Where("blocked_until > ?", time.Now()).Count(n).Error
		}},
	}
	for _, q := range queries {
		if err := q.run(q.dst); err != nil {
			return bot.ModuleStats{}, err
		}
	}

	return bot.ModuleStats{
		Summary: []bot.Stat{
			{Name: "cups", Value: strconv.FormatInt(drinks, 10)},
			{Name: "drinkers", Value: strconv.FormatInt(drinkers, 10)},
			{Name: "open orders", Value: strconv.FormatInt(openOrders, 10)},
		},
		Detail: []bot.Stat{
			{Name: "refills", Value: strconv.FormatInt(refills, 10)},
			{Name: "machines", Value: strconv.FormatInt(machines, 10)},
			{Name: "pickup violations", Value: strconv.FormatInt(violations, 10)},
			{Name: "blocked users", Value: strconv.FormatInt(blocked, 10)},
		},
	}, nil
}
