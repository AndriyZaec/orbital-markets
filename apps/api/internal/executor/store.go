package executor

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"
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
	if state == ExecStateOpen || state == ExecStateDegraded || state == ExecStateFailed {
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

// MarkClosed transitions a position to terminal "closed" state.
func (s *Store) MarkClosed(ctx context.Context, positionID string) error {
	now := time.Now().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx, `
		UPDATE live_positions SET state = ?, completed_at = COALESCE(completed_at, ?), updated_at = ?
		WHERE id = ?`,
		string(ExecStateClosed), now, now, positionID,
	)
	if err != nil {
		s.logger.Error("live store: mark closed", "err", err, "id", positionID)
	}
	return err
}

// livePositionCols is the column list for live_positions queries.
const livePositionCols = `id, plan_id, opportunity_id, asset,
	venue_a, venue_b, state,
	notional, leverage,
	entry_spread, hedge_mismatch,
	current_spread, current_basis, entry_basis, basis_change,
	price_pnl, funding_pnl, total_pnl,
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
		&p.VenueA, &p.VenueB, &p.State,
		&p.Notional, &p.Leverage,
		&p.EntrySpread, &p.HedgeMismatch,
		&p.CurrentSpread, &p.CurrentBasis, &p.EntryBasis, &p.BasisChange,
		&p.PricePnL, &p.FundingPnL, &p.TotalPnL,
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

// ListPositions returns all live positions, newest first.
func (s *Store) ListPositions(ctx context.Context) ([]LivePosition, error) {
	return s.queryPositions(ctx,
		`SELECT `+livePositionCols+` FROM live_positions ORDER BY started_at DESC`)
}

// ListOpenPositions returns positions in open or degraded state.
func (s *Store) ListOpenPositions(ctx context.Context) ([]LivePosition, error) {
	return s.queryPositions(ctx,
		`SELECT `+livePositionCols+` FROM live_positions WHERE state IN ('open', 'degraded') ORDER BY started_at DESC`)
}

func (s *Store) queryPositions(ctx context.Context, query string) ([]LivePosition, error) {
	rows, err := s.db.QueryContext(ctx, query)
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

// MonitorUpdate holds the fields written by the live monitor on each tick.
type MonitorUpdate struct {
	CurrentSpread  float64
	CurrentBasis   float64
	EntryBasis     float64
	BasisChange    float64
	PricePnL       float64
	FundingPnL     float64
	TotalPnL       float64
	Leg1CurPrice   float64
	Leg2CurPrice   float64
	Leg1LiqPrice   float64
	Leg2LiqPrice   float64
	Leg1LiqDist    float64
	Leg2LiqDist    float64
	Leg1LiqRisk    string
	Leg2LiqRisk    string
	HoldHours      float64
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
			funding_pnl = ?,
			total_pnl = ?,
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
		m.FundingPnL,
		m.TotalPnL,
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
	ID             string  `json:"id"`
	PlanID         string  `json:"plan_id"`
	OpportunityID  string  `json:"opportunity_id"`
	Asset          string  `json:"asset"`
	VenueA         string  `json:"venue_a"`
	VenueB         string  `json:"venue_b"`
	State          string  `json:"state"`
	Notional       float64 `json:"notional"`
	Leverage       float64 `json:"leverage"`
	EntrySpread    float64 `json:"entry_spread"`
	HedgeMismatch  float64 `json:"hedge_mismatch"`
	CurrentSpread  float64 `json:"current_spread"`
	CurrentBasis   float64 `json:"current_basis"`
	EntryBasis     float64 `json:"entry_basis"`
	BasisChange    float64 `json:"basis_change"`
	PricePnL       float64 `json:"price_pnl"`
	FundingPnL     float64 `json:"funding_pnl"`
	TotalPnL       float64 `json:"total_pnl"`
	Leg1CurPrice   float64 `json:"leg1_current_price"`
	Leg2CurPrice   float64 `json:"leg2_current_price"`
	Leg1LiqPrice   float64 `json:"leg1_liq_price"`
	Leg2LiqPrice   float64 `json:"leg2_liq_price"`
	Leg1LiqDist    float64 `json:"leg1_liq_dist"`
	Leg2LiqDist    float64 `json:"leg2_liq_dist"`
	Leg1LiqRisk    string  `json:"leg1_liq_risk"`
	Leg2LiqRisk    string  `json:"leg2_liq_risk"`
	HoldHours      float64 `json:"hold_hours"`
	StartedAt      string  `json:"started_at"`
	OpenedAt       string  `json:"opened_at,omitempty"`
	CompletedAt    string  `json:"completed_at,omitempty"`
	MonitorAt      string  `json:"monitor_at,omitempty"`
	UpdatedAt      string  `json:"updated_at"`
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
	s.UpdateState(ctx, posID, result.State, 0, 0)
}
