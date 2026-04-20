package paper

import (
	"context"
	"database/sql"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/db/sqlc"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
)

// DBStore persists paper positions to SQLite and keeps an in-memory cache for fast reads.
type DBStore struct {
	*Store   // embed in-memory store for fast reads
	queries *sqlc.Queries
}

func NewDBStore(database *sql.DB) *DBStore {
	return &DBStore{
		Store:   NewStore(),
		queries: sqlc.New(database),
	}
}

// Add persists a new position to both memory and DB.
func (s *DBStore) Add(pos *Position) {
	s.Store.Add(pos)
	ctx := context.Background()

	var openedAt, closedAt sql.NullString
	if pos.OpenedAt != nil {
		openedAt = sql.NullString{String: pos.OpenedAt.Format(time.RFC3339), Valid: true}
	}
	if pos.ClosedAt != nil {
		closedAt = sql.NullString{String: pos.ClosedAt.Format(time.RFC3339), Valid: true}
	}

	s.queries.InsertPosition(ctx, sqlc.InsertPositionParams{
		ID:               pos.ID,
		PlanID:           pos.PlanID,
		OpportunityID:    pos.OpportunityID,
		Asset:            pos.Asset,
		Direction:        string(pos.Direction),
		VenueA:           pos.VenuePair.VenueA,
		VenueB:           pos.VenuePair.VenueB,
		State:            string(pos.State),
		TargetNotional:   pos.TargetNotional,
		EntrySpread:      pos.EntrySpread,
		HedgeMismatch:    pos.HedgeMismatch,
		CloseReason:      string(pos.CloseReason),
		RiskTier:         string(pos.RiskTier),
		PricePnl:         pos.PricePnL,
		FundingPnl:       pos.FundingPnL,
		TotalPnl:         pos.TotalPnL,
		RealizedPnl:      pos.RealizedPnL,
		EstBreakEvenHours: pos.EstBreakEvenHours,
		BreakEvenReached:  boolToInt(pos.BreakEvenReached),
		HoldHours:        pos.HoldHours,
		CreatedAt:        pos.CreatedAt.Format(time.RFC3339),
		OpenedAt:         openedAt,
		ClosedAt:         closedAt,
		UpdatedAt:        pos.UpdatedAt.Format(time.RFC3339),
	})
}

// Update persists changes to both memory and DB.
func (s *DBStore) Update(pos *Position) {
	s.Store.Update(pos)
	ctx := context.Background()

	var openedAt, closedAt sql.NullString
	if pos.OpenedAt != nil {
		openedAt = sql.NullString{String: pos.OpenedAt.Format(time.RFC3339), Valid: true}
	}
	if pos.ClosedAt != nil {
		closedAt = sql.NullString{String: pos.ClosedAt.Format(time.RFC3339), Valid: true}
	}

	s.queries.UpdatePosition(ctx, sqlc.UpdatePositionParams{
		State:            string(pos.State),
		EntrySpread:      pos.EntrySpread,
		HedgeMismatch:    pos.HedgeMismatch,
		CloseReason:      string(pos.CloseReason),
		RiskTier:         string(pos.RiskTier),
		PricePnl:         pos.PricePnL,
		FundingPnl:       pos.FundingPnL,
		TotalPnl:         pos.TotalPnL,
		RealizedPnl:      pos.RealizedPnL,
		EstBreakEvenHours: pos.EstBreakEvenHours,
		BreakEvenReached:  boolToInt(pos.BreakEvenReached),
		HoldHours:        pos.HoldHours,
		OpenedAt:         openedAt,
		ClosedAt:         closedAt,
		UpdatedAt:        pos.UpdatedAt.Format(time.RFC3339),
		ID:               pos.ID,
	})

	// Persist fills and events
	s.persistFills(ctx, pos)
	s.persistEvents(ctx, pos)
}

func (s *DBStore) persistFills(ctx context.Context, pos *Position) {
	for legNum, fill := range []*Fill{pos.Leg1Fill, pos.Leg2Fill} {
		if fill == nil {
			continue
		}
		s.queries.InsertFill(ctx, sqlc.InsertFillParams{
			PositionID: pos.ID,
			Leg:        int64(legNum + 1),
			Venue:      fill.Venue,
			Side:       string(fill.Side),
			TargetSize: fill.TargetSize,
			FilledSize: fill.FilledSize,
			FillPrice:  fill.FillPrice,
			Slippage:   fill.Slippage,
			Fee:        fill.Fee,
			FilledAt:   fill.FilledAt.Format(time.RFC3339),
		})
	}
}

func (s *DBStore) persistEvents(ctx context.Context, pos *Position) {
	// Only persist new events (events not yet in DB)
	// Simple approach: count existing events and persist the delta
	existing, _ := s.queries.GetEventsByPosition(ctx, pos.ID)
	for i := len(existing); i < len(pos.Events); i++ {
		ev := pos.Events[i]
		s.queries.InsertEvent(ctx, sqlc.InsertEventParams{
			PositionID: pos.ID,
			FromState:  string(ev.FromState),
			ToState:    string(ev.ToState),
			Reason:     ev.Reason,
			At:         ev.At.Format(time.RFC3339),
		})
	}
}

// Analytics returns the queries for direct analytics access.
func (s *DBStore) Queries() *sqlc.Queries {
	return s.queries
}

func boolToInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// ComputeBreakEven calculates estimated break-even hours for a position.
// Formula: total_entry_cost / expected_net_funding_per_hour
func ComputeBreakEven(pos *Position) float64 {
	if pos.Leg1Fill == nil || pos.Leg2Fill == nil {
		return 0
	}

	// Total entry cost = slippage + fees on both legs
	totalEntryCost := (pos.Leg1Fill.Slippage + pos.Leg1Fill.Fee + pos.Leg2Fill.Slippage + pos.Leg2Fill.Fee) * pos.Leg1Fill.FilledSize

	// Expected net funding per hour = deannualized edge * notional
	// CurrentSpread is annualized edge
	if pos.CurrentSpread <= 0 {
		return 0
	}
	hourlyEdge := domain.DeannualizeRate(pos.CurrentSpread) * pos.Leg1Fill.FilledSize

	if hourlyEdge <= 0 {
		return 0
	}

	return totalEntryCost / hourlyEdge
}

// ComputeHoldHours returns hours since position opened.
func ComputeHoldHours(pos *Position) float64 {
	if pos.OpenedAt == nil {
		return 0
	}
	end := time.Now()
	if pos.ClosedAt != nil {
		end = *pos.ClosedAt
	}
	return end.Sub(*pos.OpenedAt).Hours()
}

func intToBool(i int64) bool {
	return i != 0
}

// LoadFromDB loads all positions from SQLite into memory on startup.
func (s *DBStore) LoadFromDB(ctx context.Context) error {
	rows, err := s.queries.ListPositions(ctx)
	if err != nil {
		return err
	}

	for _, row := range rows {
		pos := &Position{
			ID:              row.ID,
			PlanID:          row.PlanID,
			OpportunityID:   row.OpportunityID,
			Asset:           row.Asset,
			Direction:       domain.Direction(row.Direction),
			VenuePair:       domain.VenuePair{VenueA: row.VenueA, VenueB: row.VenueB},
			State:           ExecState(row.State),
			TargetNotional:  row.TargetNotional,
			EntrySpread:     row.EntrySpread,
			HedgeMismatch:   row.HedgeMismatch,
			CloseReason:     CloseReason(row.CloseReason),
			RiskTier:        domain.RiskTier(row.RiskTier),
			PricePnL:        row.PricePnl,
			FundingPnL:      row.FundingPnl,
			TotalPnL:        row.TotalPnl,
			RealizedPnL:     row.RealizedPnl,
			EstBreakEvenHours: row.EstBreakEvenHours,
			BreakEvenReached: intToBool(row.BreakEvenReached),
			HoldHours:       row.HoldHours,
		}
		pos.CreatedAt, _ = time.Parse(time.RFC3339, row.CreatedAt)
		pos.UpdatedAt, _ = time.Parse(time.RFC3339, row.UpdatedAt)
		if row.OpenedAt.Valid {
			t, _ := time.Parse(time.RFC3339, row.OpenedAt.String)
			pos.OpenedAt = &t
		}
		if row.ClosedAt.Valid {
			t, _ := time.Parse(time.RFC3339, row.ClosedAt.String)
			pos.ClosedAt = &t
		}

		// Load fills
		fills, _ := s.queries.GetFillsByPosition(ctx, pos.ID)
		for _, f := range fills {
			filledAt, _ := time.Parse(time.RFC3339, f.FilledAt)
			fill := &Fill{
				Venue:      f.Venue,
				Side:       domain.Side(f.Side),
				TargetSize: f.TargetSize,
				FilledSize: f.FilledSize,
				FillPrice:  f.FillPrice,
				Slippage:   f.Slippage,
				Fee:        f.Fee,
				FilledAt:   filledAt,
			}
			if f.Leg == 1 {
				pos.Leg1Fill = fill
			} else {
				pos.Leg2Fill = fill
			}
		}

		// Load events
		events, _ := s.queries.GetEventsByPosition(ctx, pos.ID)
		for _, e := range events {
			at, _ := time.Parse(time.RFC3339, e.At)
			pos.Events = append(pos.Events, Event{
				FromState: ExecState(e.FromState),
				ToState:   ExecState(e.ToState),
				Reason:    e.Reason,
				At:        at,
			})
		}

		s.Store.Add(pos)
	}

	return nil
}
