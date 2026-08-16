package executor

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
)

// Store persists live execution state to SQLite.
// All writes are append-friendly and safe to call during execution.
type Store struct {
	db     *sql.DB
	logger *slog.Logger
}

func NewStore(db *sql.DB, logger *slog.Logger) *Store {
	return &Store{db: db, logger: logger}
}

// CreatePosition inserts a new live position at execution start.
func (s *Store) CreatePosition(ctx context.Context, result *ExecutionResult, venueA, venueB string, notional, leverage float64) {
	now := time.Now().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO live_positions (
			id, plan_id, opportunity_id, asset,
			venue_a, venue_b, state,
			notional, leverage,
			started_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		result.PlanID,
		result.PlanID,
		result.OpportunityID,
		result.Asset,
		venueA,
		venueB,
		string(result.State),
		notional,
		leverage,
		result.StartedAt.Format(time.RFC3339),
		now,
	)
	if err != nil {
		s.logger.Error("live store: create position", "err", err, "plan_id", result.PlanID)
	}
}

// UpdateState updates the position state and timestamps.
func (s *Store) UpdateState(ctx context.Context, positionID string, state ExecState, entrySpread, hedgeMismatch float64) {
	now := time.Now().Format(time.RFC3339)
	var openedAt sql.NullString
	var completedAt sql.NullString

	if state == ExecStateOpen {
		openedAt = sql.NullString{String: now, Valid: true}
	}
	if state == ExecStateFailed {
		completedAt = sql.NullString{String: now, Valid: true}
	}

	_, err := s.db.ExecContext(ctx, `
		UPDATE live_positions SET
			state = ?,
			entry_spread = ?,
			hedge_mismatch = ?,
			opened_at = COALESCE(?, opened_at),
			completed_at = COALESCE(?, completed_at),
			updated_at = ?
		WHERE id = ?`,
		string(state),
		entrySpread,
		hedgeMismatch,
		openedAt,
		completedAt,
		now,
		positionID,
	)
	if err != nil {
		s.logger.Error("live store: update state", "err", err, "id", positionID)
	}
}

// InsertFill records a leg fill result.
func (s *Store) InsertFill(ctx context.Context, positionID string, leg int, lr LegResult) {
	now := time.Now().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO live_fills (
			position_id, leg, venue, symbol, side,
			order_id, client_order_id,
			requested_amount, filled_amount, avg_fill_price,
			fill_ratio, fee, accepted, filled, error,
			filled_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		positionID,
		leg,
		lr.Venue,
		lr.Symbol,
		lr.Side,
		lr.OrderID,
		lr.ClientOrderID,
		lr.RequestedAmt,
		lr.FilledAmount,
		lr.AvgFillPrice,
		lr.FillRatio,
		lr.Fee,
		boolToInt(lr.Accepted),
		boolToInt(lr.Filled),
		lr.Error,
		now,
	)
	if err != nil {
		s.logger.Error("live store: insert fill", "err", err, "id", positionID, "leg", leg)
	}
}

// InsertEvent records an execution lifecycle event.
func (s *Store) InsertEvent(ctx context.Context, positionID, event string, state ExecState, detail string) {
	now := time.Now().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO live_events (
			position_id, event, state, detail, at
		) VALUES (?, ?, ?, ?, ?)`,
		positionID,
		event,
		string(state),
		detail,
		now,
	)
	if err != nil {
		s.logger.Error("live store: insert event", "err", err, "id", positionID, "event", event)
	}
}

// MarkClosing transitions an open/degraded position to "closing" state.
func (s *Store) MarkClosing(ctx context.Context, positionID string) error {
	now := time.Now().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx, `
		UPDATE live_positions SET state = ?, updated_at = ?
		WHERE id = ? AND state IN ('open', 'degraded')`,
		string(ExecStateClosing), now, positionID,
	)
	if err != nil {
		s.logger.Error("live store: mark closing", "err", err, "id", positionID)
	}
	return err
}

// MarkClosed transitions a position to terminal "closed" state and reports
// whether this call performed the transition.
func (s *Store) MarkClosed(ctx context.Context, positionID string) (bool, error) {
	now := time.Now().Format(time.RFC3339)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	pricePnL, hasRealizedPnL, err := realizedClosePricePnL(ctx, tx, positionID)
	if err != nil {
		return false, err
	}
	var result sql.Result
	if hasRealizedPnL {
		result, err = tx.ExecContext(ctx, `
			UPDATE live_positions SET state = ?, completed_at = ?, price_pnl = ?,
				total_pnl = ? + funding_pnl, updated_at = ?
			WHERE id = ? AND state != ?`,
			string(ExecStateClosed), now, pricePnL, pricePnL, now, positionID, string(ExecStateClosed),
		)
	} else {
		result, err = tx.ExecContext(ctx, `
			UPDATE live_positions SET state = ?, completed_at = ?, updated_at = ?
			WHERE id = ? AND state != ?`,
			string(ExecStateClosed), now, now, positionID, string(ExecStateClosed),
		)
	}
	if err != nil {
		s.logger.Error("live store: mark closed", "err", err, "id", positionID)
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return rows > 0, nil
}

const closePnLCoverageTolerance = 0.005

func realizedClosePricePnL(ctx context.Context, tx *sql.Tx, positionID string) (float64, bool, error) {
	type openingLeg struct {
		leg        int
		side       string
		amount     float64
		entryPrice float64
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT leg, side, filled_amount, avg_fill_price
		FROM live_fills
		WHERE position_id = ? AND filled = 1
		ORDER BY leg`, positionID)
	if err != nil {
		return 0, false, err
	}
	var legs []openingLeg
	for rows.Next() {
		var leg openingLeg
		if err := rows.Scan(&leg.leg, &leg.side, &leg.amount, &leg.entryPrice); err != nil {
			rows.Close()
			return 0, false, err
		}
		legs = append(legs, leg)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, false, err
	}
	if err := rows.Close(); err != nil {
		return 0, false, err
	}
	if len(legs) == 0 {
		return 0, false, nil
	}

	var total float64
	for _, leg := range legs {
		var closeAmount, weightedClosePrice sql.NullFloat64
		if err := tx.QueryRowContext(ctx, `
			SELECT SUM(filled_amount), SUM(filled_amount * avg_fill_price)
			FROM live_close_outcomes
			WHERE position_id = ? AND leg = ? AND resolved = 1
				AND filled_amount > 0 AND avg_fill_price > 0`, positionID, leg.leg,
		).Scan(&closeAmount, &weightedClosePrice); err != nil {
			return 0, false, err
		}
		if !closeAmount.Valid || !weightedClosePrice.Valid || leg.amount <= 0 || leg.entryPrice <= 0 {
			return 0, false, nil
		}
		tolerance := math.Max(leg.amount*closePnLCoverageTolerance, 1e-9)
		if math.Abs(closeAmount.Float64-leg.amount) > tolerance {
			return 0, false, nil
		}
		closePrice := weightedClosePrice.Float64 / closeAmount.Float64
		side := domain.Side(strings.ToLower(leg.side))
		if side != domain.SideLong && side != domain.SideShort {
			return 0, false, nil
		}
		total += legPricePnL(side, leg.entryPrice, closePrice, leg.amount)
	}
	return total, true, nil
}

// CloseOutcome records the latest known state of a reduce-only close order.
type CloseOutcome struct {
	PositionID      string
	Leg             int
	Venue           string
	ClientOrderID   string
	OrderID         string
	RequestedAmount float64
	FilledAmount    float64
	AvgFillPrice    float64
	FillRatio       float64
	Accepted        bool
	Confirmed       bool
	Resolved        bool
	Error           string
}

// CloseProgress summarizes the latest close attempt for each original open leg.
type CloseProgress struct {
	Required  int
	Confirmed int
	Failed    int
	Pending   int
}

// UpsertCloseOutcome persists submission acceptance and later fill resolution.
func (s *Store) UpsertCloseOutcome(ctx context.Context, outcome CloseOutcome) error {
	now := time.Now().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO live_close_outcomes (
			position_id, leg, venue, client_order_id, order_id,
			requested_amount, filled_amount, avg_fill_price, fill_ratio,
			accepted, confirmed, resolved, error, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(position_id, client_order_id) DO UPDATE SET
			order_id = excluded.order_id,
			filled_amount = excluded.filled_amount,
			avg_fill_price = excluded.avg_fill_price,
			fill_ratio = excluded.fill_ratio,
			accepted = excluded.accepted,
			confirmed = excluded.confirmed,
			resolved = excluded.resolved,
			error = excluded.error,
			updated_at = excluded.updated_at`,
		outcome.PositionID, outcome.Leg, outcome.Venue, outcome.ClientOrderID, outcome.OrderID,
		outcome.RequestedAmount, outcome.FilledAmount, outcome.AvgFillPrice, outcome.FillRatio,
		boolToInt(outcome.Accepted), boolToInt(outcome.Confirmed), boolToInt(outcome.Resolved), outcome.Error,
		now, now,
	)
	if err != nil {
		s.logger.Error("live store: upsert close outcome", "err", err, "id", outcome.PositionID, "leg", outcome.Leg)
	}
	return err
}

// GetCloseProgress compares the latest close attempt for each leg with the
// original confirmed legs that must be closed.
func (s *Store) GetCloseProgress(ctx context.Context, positionID string) (CloseProgress, error) {
	var progress CloseProgress
	err := s.db.QueryRowContext(ctx, `
		WITH required AS (
			SELECT COUNT(DISTINCT leg) AS total
			FROM live_fills
			WHERE position_id = ? AND filled = 1
		), latest AS (
			SELECT outcome.leg, outcome.confirmed, outcome.resolved
			FROM live_close_outcomes outcome
			JOIN (
				SELECT leg, MAX(id) AS id
				FROM live_close_outcomes
				WHERE position_id = ?
				GROUP BY leg
			) current ON current.id = outcome.id
		)
		SELECT required.total,
			COALESCE(SUM(CASE WHEN latest.confirmed = 1 THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN latest.resolved = 1 AND latest.confirmed = 0 THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN latest.resolved = 0 THEN 1 ELSE 0 END), 0)
		FROM required LEFT JOIN latest ON 1 = 1
		GROUP BY required.total`, positionID, positionID).Scan(
		&progress.Required, &progress.Confirmed, &progress.Failed, &progress.Pending,
	)
	return progress, err
}

// ConfirmedCloseLegs returns legs already confirmed closed, so retries do not
// submit a second reduce-only order for them.
func (s *Store) ConfirmedCloseLegs(ctx context.Context, positionID string) (map[int]bool, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT outcome.leg
		FROM live_close_outcomes outcome
		JOIN (
			SELECT leg, MAX(id) AS id
			FROM live_close_outcomes
			WHERE position_id = ?
			GROUP BY leg
		) current ON current.id = outcome.id
		WHERE outcome.confirmed = 1`, positionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	legs := make(map[int]bool)
	for rows.Next() {
		var leg int
		if err := rows.Scan(&leg); err != nil {
			return nil, err
		}
		legs[leg] = true
	}
	return legs, rows.Err()
}

func (s *Store) LastCloseActivity(ctx context.Context, positionID string) (time.Time, error) {
	var raw sql.NullString
	if err := s.db.QueryRowContext(ctx, `
		SELECT MAX(updated_at) FROM live_close_outcomes WHERE position_id = ?`, positionID,
	).Scan(&raw); err != nil {
		return time.Time{}, err
	}
	if !raw.Valid || raw.String == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339, raw.String)
}

// MarkCloseDegraded records a terminal close failure without overwriting
// monitoring fields from the open position.
func (s *Store) MarkCloseDegraded(ctx context.Context, positionID string) error {
	now := time.Now().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx, `
		UPDATE live_positions SET state = ?, updated_at = ? WHERE id = ? AND state != ?`,
		string(ExecStateDegraded), now, positionID, string(ExecStateClosed),
	)
	if err != nil {
		s.logger.Error("live store: mark close degraded", "err", err, "id", positionID)
	}
	return err
}

// livePositionCols is the column list for live_positions queries.
const livePositionCols = `id, plan_id, opportunity_id, asset,
	venue_a, venue_b, state, account_pacifica, account_hyperliquid,
	notional, leverage,
	entry_spread, hedge_mismatch,
	current_spread, current_basis, entry_basis, basis_change,
	price_pnl, funding_pnl, funding_pnl_source, total_pnl,
	leg1_current_price, leg2_current_price,
	leg1_liq_price, leg2_liq_price,
	leg1_liq_dist, leg2_liq_dist,
	leg1_liq_risk, leg2_liq_risk,
	hold_hours,
	started_at, opened_at, completed_at, monitor_at, updated_at`

func scanLivePosition(scanner interface{ Scan(...any) error }) (*LivePosition, error) {
	var p LivePosition
	var openedAt, completedAt, monitorAt sql.NullString
	err := scanner.Scan(
		&p.ID, &p.PlanID, &p.OpportunityID, &p.Asset,
		&p.VenueA, &p.VenueB, &p.State, &p.AccountPacifica, &p.AccountHyperliquid,
		&p.Notional, &p.Leverage,
		&p.EntrySpread, &p.HedgeMismatch,
		&p.CurrentSpread, &p.CurrentBasis, &p.EntryBasis, &p.BasisChange,
		&p.PricePnL, &p.FundingPnL, &p.FundingPnLSource, &p.TotalPnL,
		&p.Leg1CurPrice, &p.Leg2CurPrice,
		&p.Leg1LiqPrice, &p.Leg2LiqPrice,
		&p.Leg1LiqDist, &p.Leg2LiqDist,
		&p.Leg1LiqRisk, &p.Leg2LiqRisk,
		&p.HoldHours,
		&p.StartedAt, &openedAt, &completedAt, &monitorAt, &p.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	p.OpenedAt = openedAt.String
	p.CompletedAt = completedAt.String
	p.MonitorAt = monitorAt.String
	return &p, nil
}

// GetPosition returns a live position by ID.
func (s *Store) GetPosition(ctx context.Context, id string) (*LivePosition, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+livePositionCols+` FROM live_positions WHERE id = ?`, id)
	return scanLivePosition(row)
}

func (s *Store) GetPositionForAccounts(ctx context.Context, id, pacifica, hyperliquid string) (*LivePosition, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+livePositionCols+` FROM live_positions
		 WHERE id = ? AND account_pacifica = ? AND account_hyperliquid = ?`,
		id, strings.TrimSpace(pacifica), strings.ToLower(strings.TrimSpace(hyperliquid)))
	return scanLivePosition(row)
}

// ListPositions returns all live positions, newest first.
func (s *Store) ListPositions(ctx context.Context) ([]LivePosition, error) {
	return s.queryPositions(ctx,
		`SELECT `+livePositionCols+` FROM live_positions ORDER BY started_at DESC`)
}

func (s *Store) ListPositionsForAccounts(ctx context.Context, pacifica, hyperliquid string) ([]LivePosition, error) {
	return s.queryPositions(ctx,
		`SELECT `+livePositionCols+` FROM live_positions
		 WHERE account_pacifica = ? AND account_hyperliquid = ? ORDER BY started_at DESC`,
		strings.TrimSpace(pacifica), strings.ToLower(strings.TrimSpace(hyperliquid)))
}

// ListOpenPositions returns positions in open or degraded state.
func (s *Store) ListOpenPositions(ctx context.Context) ([]LivePosition, error) {
	return s.queryPositions(ctx,
		`SELECT `+livePositionCols+` FROM live_positions WHERE state IN ('open', 'degraded') ORDER BY started_at DESC`)
}

func (s *Store) ListOpenPositionsForAccounts(ctx context.Context, pacifica, hyperliquid string) ([]LivePosition, error) {
	return s.queryPositions(ctx,
		`SELECT `+livePositionCols+` FROM live_positions
		 WHERE state IN ('open', 'degraded') AND account_pacifica = ? AND account_hyperliquid = ?
		 ORDER BY started_at DESC`,
		strings.TrimSpace(pacifica), strings.ToLower(strings.TrimSpace(hyperliquid)))
}

// ListCloseablePositionsForAccounts includes interrupted closes so emergency
// recovery remains available after an API restart.
func (s *Store) ListCloseablePositionsForAccounts(ctx context.Context, pacifica, hyperliquid string) ([]LivePosition, error) {
	return s.queryPositions(ctx,
		`SELECT `+livePositionCols+` FROM live_positions
		 WHERE state IN ('open', 'degraded', 'closing') AND account_pacifica = ? AND account_hyperliquid = ?
		 ORDER BY started_at DESC`,
		strings.TrimSpace(pacifica), strings.ToLower(strings.TrimSpace(hyperliquid)))
}

func (s *Store) ListClosingPositions(ctx context.Context) ([]LivePosition, error) {
	return s.queryPositions(ctx,
		`SELECT `+livePositionCols+` FROM live_positions
		 WHERE state = 'closing' ORDER BY started_at DESC`)
}

func (s *Store) queryPositions(ctx context.Context, query string, args ...any) ([]LivePosition, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var positions []LivePosition
	for rows.Next() {
		p, err := scanLivePosition(rows)
		if err != nil {
			return nil, err
		}
		positions = append(positions, *p)
	}
	return positions, rows.Err()
}

// GetFills returns all fills for a position.
func (s *Store) GetFills(ctx context.Context, positionID string) ([]LiveFill, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, position_id, leg, venue, symbol, side,
			order_id, client_order_id,
			requested_amount, filled_amount, avg_fill_price,
			fill_ratio, fee, accepted, filled, error, filled_at
		FROM live_fills WHERE position_id = ? ORDER BY leg`, positionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var fills []LiveFill
	for rows.Next() {
		var f LiveFill
		if err := rows.Scan(
			&f.ID, &f.PositionID, &f.Leg, &f.Venue, &f.Symbol, &f.Side,
			&f.OrderID, &f.ClientOrderID,
			&f.RequestedAmount, &f.FilledAmount, &f.AvgFillPrice,
			&f.FillRatio, &f.Fee, &f.Accepted, &f.Filled, &f.Error, &f.FilledAt,
		); err != nil {
			return nil, err
		}
		fills = append(fills, f)
	}
	return fills, rows.Err()
}

// GetEvents returns all events for a position.
func (s *Store) GetEvents(ctx context.Context, positionID string) ([]LiveEvent, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, position_id, event, state, detail, at
		FROM live_events WHERE position_id = ? ORDER BY id`, positionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []LiveEvent
	for rows.Next() {
		var e LiveEvent
		if err := rows.Scan(
			&e.ID, &e.PositionID, &e.Event, &e.State, &e.Detail, &e.At,
		); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

func (s *Store) InsertFundingPayments(ctx context.Context, positionID string, payments []venue.FundingPayment) error {
	if len(payments) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, payment := range payments {
		if payment.ExternalID == "" || payment.Venue == "" || payment.Account == "" || payment.Asset == "" || payment.PaidAt.IsZero() {
			return fmt.Errorf("invalid funding payment")
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO live_funding_payments (
				position_id, venue, account, external_id, asset, amount_usd, paid_at, created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(venue, account, external_id) DO NOTHING`,
			positionID, payment.Venue, payment.Account, payment.ExternalID, payment.Asset, payment.AmountUSD,
			payment.PaidAt.UTC().Format(time.RFC3339Nano), now,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) SumFundingPayments(ctx context.Context, positionID string) (float64, error) {
	var total float64
	err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(amount_usd), 0)
		FROM live_funding_payments WHERE position_id = ?`, positionID).Scan(&total)
	return total, err
}

func (s *Store) RecordFundingSync(ctx context.Context, positionID string, finalized bool) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO live_funding_sync (position_id, synced_at, finalized)
		VALUES (?, ?, ?)
		ON CONFLICT(position_id) DO UPDATE SET
			synced_at = excluded.synced_at,
			finalized = MAX(live_funding_sync.finalized, excluded.finalized)`,
		positionID, time.Now().UTC().Format(time.RFC3339Nano), boolToInt(finalized),
	)
	return err
}

func (s *Store) ListUnfinalizedClosedPositions(ctx context.Context) ([]LivePosition, error) {
	return s.queryPositions(ctx, `
		SELECT `+livePositionCols+` FROM live_positions
		WHERE state = 'closed' AND NOT EXISTS (
			SELECT 1 FROM live_funding_sync funding
			WHERE funding.position_id = live_positions.id AND funding.finalized = 1
		)
		ORDER BY updated_at`)
}

func (s *Store) UpdateRealizedFunding(ctx context.Context, positionID string, fundingPnL float64) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx, `
		UPDATE live_positions SET funding_pnl = ?, funding_pnl_source = 'realized',
			total_pnl = price_pnl + ?, monitor_at = ?, updated_at = ?
		WHERE id = ?`, fundingPnL, fundingPnL, now, now, positionID)
	return err
}

// MonitorUpdate holds the fields written by the live monitor on each tick.
type MonitorUpdate struct {
	CurrentSpread float64
	CurrentBasis  float64
	EntryBasis    float64
	BasisChange   float64
	PricePnL      float64
	Leg1CurPrice  float64
	Leg2CurPrice  float64
	Leg1LiqPrice  float64
	Leg2LiqPrice  float64
	Leg1LiqDist   float64
	Leg2LiqDist   float64
	Leg1LiqRisk   string
	Leg2LiqRisk   string
	HoldHours     float64
}

// UpdateMonitoring writes monitoring-derived fields to a live position.
func (s *Store) UpdateMonitoring(ctx context.Context, positionID string, m MonitorUpdate) {
	now := time.Now().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx, `
		UPDATE live_positions SET
			current_spread = ?,
			current_basis = ?,
			entry_basis = ?,
			basis_change = ?,
			price_pnl = ?,
			total_pnl = ? + funding_pnl,
			leg1_current_price = ?,
			leg2_current_price = ?,
			leg1_liq_price = ?,
			leg2_liq_price = ?,
			leg1_liq_dist = ?,
			leg2_liq_dist = ?,
			leg1_liq_risk = ?,
			leg2_liq_risk = ?,
			hold_hours = ?,
			monitor_at = ?,
			updated_at = ?
		WHERE id = ?`,
		m.CurrentSpread,
		m.CurrentBasis,
		m.EntryBasis,
		m.BasisChange,
		m.PricePnL,
		m.PricePnL,
		m.Leg1CurPrice,
		m.Leg2CurPrice,
		m.Leg1LiqPrice,
		m.Leg2LiqPrice,
		m.Leg1LiqDist,
		m.Leg2LiqDist,
		m.Leg1LiqRisk,
		m.Leg2LiqRisk,
		m.HoldHours,
		now,
		now,
		positionID,
	)
	if err != nil {
		s.logger.Error("live store: update monitoring", "err", err, "id", positionID)
	}
}

// LivePosition is the read model for a live position.
type LivePosition struct {
	ID                 string  `json:"id"`
	PlanID             string  `json:"plan_id"`
	OpportunityID      string  `json:"opportunity_id"`
	Asset              string  `json:"asset"`
	VenueA             string  `json:"venue_a"`
	VenueB             string  `json:"venue_b"`
	State              string  `json:"state"`
	AccountPacifica    string  `json:"-"`
	AccountHyperliquid string  `json:"-"`
	Notional           float64 `json:"notional"`
	Leverage           float64 `json:"leverage"`
	EntrySpread        float64 `json:"entry_spread"`
	HedgeMismatch      float64 `json:"hedge_mismatch"`
	CurrentSpread      float64 `json:"current_spread"`
	CurrentBasis       float64 `json:"current_basis"`
	EntryBasis         float64 `json:"entry_basis"`
	BasisChange        float64 `json:"basis_change"`
	PricePnL           float64 `json:"price_pnl"`
	FundingPnL         float64 `json:"funding_pnl"`
	FundingPnLSource   string  `json:"funding_pnl_source"`
	TotalPnL           float64 `json:"total_pnl"`
	Leg1CurPrice       float64 `json:"leg1_current_price"`
	Leg2CurPrice       float64 `json:"leg2_current_price"`
	Leg1LiqPrice       float64 `json:"leg1_liq_price"`
	Leg2LiqPrice       float64 `json:"leg2_liq_price"`
	Leg1LiqDist        float64 `json:"leg1_liq_dist"`
	Leg2LiqDist        float64 `json:"leg2_liq_dist"`
	Leg1LiqRisk        string  `json:"leg1_liq_risk"`
	Leg2LiqRisk        string  `json:"leg2_liq_risk"`
	HoldHours          float64 `json:"hold_hours"`
	StartedAt          string  `json:"started_at"`
	OpenedAt           string  `json:"opened_at,omitempty"`
	CompletedAt        string  `json:"completed_at,omitempty"`
	MonitorAt          string  `json:"monitor_at,omitempty"`
	UpdatedAt          string  `json:"updated_at"`
}

// LiveFill is the read model for a leg fill.
type LiveFill struct {
	ID              int64   `json:"id"`
	PositionID      string  `json:"position_id"`
	Leg             int     `json:"leg"`
	Venue           string  `json:"venue"`
	Symbol          string  `json:"symbol"`
	Side            string  `json:"side"`
	OrderID         string  `json:"order_id"`
	ClientOrderID   string  `json:"client_order_id"`
	RequestedAmount float64 `json:"requested_amount"`
	FilledAmount    float64 `json:"filled_amount"`
	AvgFillPrice    float64 `json:"avg_fill_price"`
	FillRatio       float64 `json:"fill_ratio"`
	Fee             float64 `json:"fee"`
	Accepted        bool    `json:"accepted"`
	Filled          bool    `json:"filled"`
	Error           string  `json:"error,omitempty"`
	FilledAt        string  `json:"filled_at"`
}

// LiveEvent is the read model for an execution event.
type LiveEvent struct {
	ID         int64  `json:"id"`
	PositionID string `json:"position_id"`
	Event      string `json:"event"`
	State      string `json:"state"`
	Detail     string `json:"detail,omitempty"`
	At         string `json:"at"`
}

func boolToInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func resultHedgeMismatch(result *ExecutionResult) float64 {
	leg1Amount := result.Leg1.FilledAmount
	if leg1Amount <= 0 {
		return 1
	}
	return math.Abs(result.Leg2.FilledAmount-leg1Amount) / leg1Amount
}

// ReasonsDetail joins reasons into a single string for event detail.
func ReasonsDetail(reasons []string) string {
	if len(reasons) == 0 {
		return ""
	}
	b, _ := json.Marshal(reasons)
	return string(b)
}

// PersistFullResult writes the complete execution result as a single position
// with fills, events, and final state. Called at the end of execution.
func (s *Store) PersistFullResult(ctx context.Context, result *ExecutionResult, venueA, venueB string, notional, leverage float64) {
	posID := result.PlanID

	// Position
	s.CreatePosition(ctx, result, venueA, venueB, notional, leverage)

	// Leg 1 fill
	if result.Leg1.Submitted {
		s.InsertFill(ctx, posID, 1, result.Leg1)
		s.InsertEvent(ctx, posID, "leg1_submit", result.State,
			fmt.Sprintf("venue=%s symbol=%s side=%s", result.Leg1.Venue, result.Leg1.Symbol, result.Leg1.Side))
		if result.Leg1.Filled {
			s.InsertEvent(ctx, posID, "leg1_fill", result.State,
				fmt.Sprintf("filled=%.4f price=%.4f ratio=%.1f%%",
					result.Leg1.FilledAmount, result.Leg1.AvgFillPrice, result.Leg1.FillRatio*100))
		}
	}

	// Leg 2 fill
	if result.Leg2.Submitted {
		s.InsertFill(ctx, posID, 2, result.Leg2)
		s.InsertEvent(ctx, posID, "leg2_submit", result.State,
			fmt.Sprintf("venue=%s symbol=%s side=%s", result.Leg2.Venue, result.Leg2.Symbol, result.Leg2.Side))
		if result.Leg2.Filled {
			s.InsertEvent(ctx, posID, "leg2_fill", result.State,
				fmt.Sprintf("filled=%.4f price=%.4f ratio=%.1f%%",
					result.Leg2.FilledAmount, result.Leg2.AvgFillPrice, result.Leg2.FillRatio*100))
		}
	}

	// Recovery actions
	for _, ra := range result.Recovery {
		s.InsertEvent(ctx, posID, ra.Action, result.State,
			fmt.Sprintf("success=%v detail=%s", ra.Success, ra.Detail))
	}

	// Final state
	s.InsertEvent(ctx, posID, "complete", result.State, ReasonsDetail(result.Reasons))
	s.UpdateState(ctx, posID, result.State, 0, resultHedgeMismatch(result))
}
