package coffee

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"gorm.io/gorm"
)

const (
	orderStatusReady    = "ready"
	orderStatusBrewing  = "brewing"
	orderStatusPickedUp = "picked_up"
	orderStatusExpired  = "expired"

	pickupWindow       = 20 * time.Minute
	violationWindow    = 90 * 24 * time.Hour
	orderSweepInterval = time.Minute
)

var (
	banDurations        = [...]time.Duration{0, 3 * 24 * time.Hour, 7 * 24 * time.Hour, 30 * 24 * time.Hour}
	probationDurations  = [...]time.Duration{0, 7 * 24 * time.Hour, 14 * 24 * time.Hour, 28 * 24 * time.Hour}
	violationThresholds = [...]int{3, 2, 1, 1}
)

type restrictionStatus struct {
	BlockedUntil time.Time
}

type pickupResult struct {
	order   DrinkOrder
	picked  bool
	expired bool
}

func (s restrictionStatus) blocked(now time.Time) bool {
	return !s.BlockedUntil.IsZero() && now.Before(s.BlockedUntil)
}

// normalizeRestrictionTx clears a completed probation cycle. Violations from
// that successfully completed cycle must not count towards a later first ban.
func normalizeRestrictionTx(tx *gorm.DB, userID string, now time.Time) (BrewRestriction, error) {
	var state BrewRestriction
	result := tx.Where("user_id = ?", userID).Limit(1).Find(&state)
	if result.Error != nil {
		return BrewRestriction{}, result.Error
	}
	if result.RowsAffected == 0 {
		return BrewRestriction{}, nil
	}
	if !state.ProbationUntil.IsZero() && !now.Before(state.ProbationUntil) {
		if err := tx.Where("user_id = ?", userID).Delete(&PickupViolation{}).Error; err != nil {
			return BrewRestriction{}, err
		}
		if err := tx.Unscoped().Delete(&state).Error; err != nil {
			return BrewRestriction{}, err
		}
		return BrewRestriction{}, nil
	}
	return state, nil
}

func restrictionStatusTx(tx *gorm.DB, userID string, now time.Time) (restrictionStatus, error) {
	state, err := normalizeRestrictionTx(tx, userID, now)
	if err != nil {
		return restrictionStatus{}, err
	}
	return restrictionStatus{BlockedUntil: state.BlockedUntil}, nil
}

func (m *Module) restrictionForUser(userID string, now time.Time) (restrictionStatus, error) {
	db := m.getDB()
	if db == nil {
		return restrictionStatus{}, errors.New("store not initialized")
	}
	m.machineMu.Lock()
	defer m.machineMu.Unlock()
	var status restrictionStatus
	err := db.Transaction(func(tx *gorm.DB) error {
		var err error
		status, err = restrictionStatusTx(tx, userID, now)
		return err
	})
	return status, err
}

// recordPickupViolationTx records one missed pickup and advances the user's ban
// stage when the threshold for their current cycle is reached.
func recordPickupViolationTx(tx *gorm.DB, orderID uint, userID string, occurredAt time.Time) error {
	state, err := normalizeRestrictionTx(tx, userID, occurredAt)
	if err != nil {
		return err
	}
	violation := PickupViolation{UserID: userID, OrderID: orderID, OccurredAt: occurredAt}
	if err = tx.Create(&violation).Error; err != nil {
		return err
	}
	stage := state.Stage
	if stage < 0 || stage >= len(violationThresholds) {
		stage = 0
	}
	cutoff := occurredAt.Add(-violationWindow)
	if state.CycleStartedAt.After(cutoff) {
		cutoff = state.CycleStartedAt
	}
	var count int64
	if err = tx.Model(&PickupViolation{}).
		Where("user_id = ? AND occurred_at > ?", userID, cutoff).
		Count(&count).Error; err != nil {
		return err
	}
	if count < int64(violationThresholds[stage]) {
		return nil
	}

	nextStage := stage + 1
	if nextStage >= len(banDurations) {
		nextStage = len(banDurations) - 1
	}
	state.UserID = userID
	state.Stage = nextStage
	state.CycleStartedAt = occurredAt
	state.BlockedUntil = occurredAt.Add(banDurations[nextStage])
	state.ProbationUntil = state.BlockedUntil.Add(probationDurations[nextStage])
	return tx.Save(&state).Error
}

// expireOrderTx atomically expires one due order and records exactly one violation.
func expireOrderTx(tx *gorm.DB, order *DrinkOrder, now time.Time) (bool, error) {
	if order.Status != orderStatusReady || now.Before(order.ExpiresAt) {
		return false, nil
	}
	result := tx.Model(&DrinkOrder{}).
		Where("id = ? AND status = ?", order.ID, orderStatusReady).
		Updates(map[string]any{"status": orderStatusExpired, "expired_at": now})
	if result.Error != nil || result.RowsAffected == 0 {
		return false, result.Error
	}
	if err := recordPickupViolationTx(tx, order.ID, order.UserID, order.ExpiresAt); err != nil {
		return false, err
	}
	return true, nil
}

func (m *Module) expireDueOrders(now time.Time) (int, error) {
	db := m.getDB()
	if db == nil {
		return 0, errors.New("store not initialized")
	}
	m.machineMu.Lock()
	defer m.machineMu.Unlock()
	var expired int
	err := db.Transaction(func(tx *gorm.DB) error {
		// A restart can interrupt the short brewing animation. Release those orders
		// without penalizing the user because no pickup button was ever guaranteed.
		if err := tx.Model(&DrinkOrder{}).
			Where("status = ? AND ready_at <= ?", orderStatusBrewing, now).
			Updates(map[string]any{"status": orderStatusExpired, "expired_at": now}).Error; err != nil {
			return err
		}
		var orders []DrinkOrder
		if err := tx.Where("status = ? AND expires_at <= ?", orderStatusReady, now).Find(&orders).Error; err != nil {
			return err
		}
		for idx := range orders {
			didExpire, err := expireOrderTx(tx, &orders[idx], now)
			if err != nil {
				return err
			}
			if didExpire {
				expired++
			}
		}
		return nil
	})
	return expired, err
}

func (m *Module) pickupOrder(orderID uint, userID string, now time.Time) (pickupResult, error) {
	db := m.getDB()
	if db == nil {
		return pickupResult{}, errors.New("store not initialized")
	}
	m.machineMu.Lock()
	defer m.machineMu.Unlock()
	var out pickupResult
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&out.order, orderID).Error; err != nil {
			return err
		}
		if out.order.UserID != userID {
			return nil
		}
		if out.order.Status == orderStatusExpired {
			out.expired = true
			return nil
		}
		if out.order.Status != orderStatusReady {
			return nil
		}
		if !now.Before(out.order.ExpiresAt) {
			expired, err := expireOrderTx(tx, &out.order, now)
			out.expired = expired
			return err
		}
		result := tx.Model(&DrinkOrder{}).
			Where("id = ? AND status = ?", orderID, orderStatusReady).
			Updates(map[string]any{"status": orderStatusPickedUp, "picked_up_at": now})
		if result.Error != nil {
			return result.Error
		}
		out.picked = result.RowsAffected == 1
		return nil
	})
	return out, err
}

func (m *Module) markOrderReady(orderID uint, now time.Time) (DrinkOrder, error) {
	db := m.getDB()
	if db == nil {
		return DrinkOrder{}, errors.New("store not initialized")
	}
	m.machineMu.Lock()
	defer m.machineMu.Unlock()
	result := db.Model(&DrinkOrder{}).
		Where("id = ? AND status = ?", orderID, orderStatusBrewing).
		Updates(map[string]any{"status": orderStatusReady, "ready_at": now, "expires_at": now.Add(pickupWindow)})
	if result.Error != nil {
		return DrinkOrder{}, result.Error
	}
	if result.RowsAffected != 1 {
		return DrinkOrder{}, errors.New("order is no longer brewing")
	}
	var order DrinkOrder
	return order, db.First(&order, orderID).Error
}

func (m *Module) runOrderExpiry(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	default:
	}
	if count, err := m.expireDueOrders(m.nowFunc().UTC()); err != nil {
		slog.Error("coffee: initial order expiry sweep failed", "error", err)
	} else if count > 0 {
		slog.Info("coffee: expired unclaimed drinks", "count", count)
	}
	ticker := time.NewTicker(orderSweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if count, err := m.expireDueOrders(m.nowFunc().UTC()); err != nil {
				slog.Error("coffee: order expiry sweep failed", "error", err)
			} else if count > 0 {
				slog.Info("coffee: expired unclaimed drinks", "count", count)
			}
		}
	}
}

func formatRestriction(until, now time.Time) string {
	remaining := until.Sub(now).Round(time.Minute)
	if remaining < time.Minute {
		remaining = time.Minute
	}
	return fmt.Sprintf("You cannot use `/brew` until <t:%d:F> (%s remaining) because too many drinks were left unclaimed.", until.Unix(), remaining)
}
