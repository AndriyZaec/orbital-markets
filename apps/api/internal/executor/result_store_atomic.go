package executor

import (
	"context"
	"fmt"
	"time"
)

// PersistFullResultAtomic writes the terminal position, fills, and events in a
// single transaction. Durable sessions must not be finalized after a partial
// result write.
func (s *Store) PersistFullResultAtomic(
	ctx context.Context,
	result *ExecutionResult,
	venueA, venueB string,
	notional, leverage float64,
) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	now := time.Now().UTC().Format(time.RFC3339Nano)
	var openedAt, completedAt any
	if result.State == ExecStateOpen {
		openedAt = now
	}
	if result.State == ExecStateOpen || result.State == ExecStateDegraded || result.State == ExecStateFailed {
		completedAt = now
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO live_positions (
			id, plan_id, opportunity_id, asset, venue_a, venue_b, state,
			notional, leverage, entry_spread, hedge_mismatch,
			started_at, opened_at, completed_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0, 0, ?, ?, ?, ?)`,
		result.PlanID, result.PlanID, result.OpportunityID, result.Asset,
		venueA, venueB, string(result.State), notional, leverage,
		result.StartedAt.UTC().Format(time.RFC3339Nano), openedAt, completedAt, now,
	)
	if err != nil {
		return fmt.Errorf("insert live position: %w", err)
	}

	insertEvent := func(event string, detail string) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO live_events (position_id, event, state, detail, at)
			VALUES (?, ?, ?, ?, ?)`, result.PlanID, event, string(result.State), detail, now)
		return err
	}
	insertLeg := func(leg int, legResult LegResult) error {
		if !legResult.Submitted {
			return nil
		}
		_, err := tx.ExecContext(ctx, `
			INSERT INTO live_fills (
				position_id, leg, venue, symbol, side, order_id, client_order_id,
				requested_amount, filled_amount, avg_fill_price, fill_ratio, fee,
				accepted, filled, error, filled_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			result.PlanID, leg, legResult.Venue, legResult.Symbol, legResult.Side,
			legResult.OrderID, legResult.ClientOrderID, legResult.RequestedAmt,
			legResult.FilledAmount, legResult.AvgFillPrice, legResult.FillRatio, legResult.Fee,
			boolToInt(legResult.Accepted), boolToInt(legResult.Filled), legResult.Error, now,
		)
		if err != nil {
			return err
		}
		if err := insertEvent(fmt.Sprintf("leg%d_submit", leg),
			fmt.Sprintf("venue=%s symbol=%s side=%s", legResult.Venue, legResult.Symbol, legResult.Side)); err != nil {
			return err
		}
		if legResult.Filled {
			return insertEvent(fmt.Sprintf("leg%d_fill", leg),
				fmt.Sprintf("filled=%.4f price=%.4f ratio=%.1f%%",
					legResult.FilledAmount, legResult.AvgFillPrice, legResult.FillRatio*100))
		}
		return nil
	}

	if err := insertLeg(1, result.Leg1); err != nil {
		return fmt.Errorf("persist leg 1: %w", err)
	}
	if err := insertLeg(2, result.Leg2); err != nil {
		return fmt.Errorf("persist leg 2: %w", err)
	}
	for _, recovery := range result.Recovery {
		if err := insertEvent(recovery.Action,
			fmt.Sprintf("success=%v detail=%s", recovery.Success, recovery.Detail)); err != nil {
			return fmt.Errorf("persist recovery event: %w", err)
		}
	}
	if err := insertEvent("complete", ReasonsDetail(result.Reasons)); err != nil {
		return fmt.Errorf("persist completion event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}
