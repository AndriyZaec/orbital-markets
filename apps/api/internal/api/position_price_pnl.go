package api

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/executor"
)

type positionPricePnLPoint struct {
	T        string   `json:"t"`
	Segment  *int     `json:"segment,omitempty"`
	LongPnL  *float64 `json:"long_pnl"`
	ShortPnL *float64 `json:"short_pnl"`
	PricePnL *float64 `json:"price_pnl"`
}

type positionPricePnLContext struct {
	Available    bool                   `json:"available"`
	Reason       string                 `json:"reason,omitempty"`
	LongVenue    string                 `json:"long_venue,omitempty"`
	ShortVenue   string                 `json:"short_venue,omitempty"`
	OpenedAt     string                 `json:"opened_at,omitempty"`
	Latest       *positionPricePnLPoint `json:"latest,omitempty"`
	ValuationKey string                 `json:"valuation_key,omitempty"`
}

func (m *positionPricePnLModel) key() string {
	return fmt.Sprintf("%d:%s:%.17g:%.17g:%d:%s:%.17g:%.17g:%s", m.leg1.ID, m.leg1.Side, m.leg1.FilledAmount, m.leg1.AvgFillPrice,
		m.leg2.ID, m.leg2.Side, m.leg2.FilledAmount, m.leg2.AvgFillPrice, m.openedAt.UTC().Format(time.RFC3339Nano))
}

type positionPricePnLModel struct {
	leg1, leg2            executor.LiveFill
	openedAt              time.Time
	longVenue, shortVenue string
}

func newPositionPricePnLModel(pos *executor.LivePosition, fills []executor.LiveFill) (*positionPricePnLModel, string) {
	if pos == nil || pos.State != "open" {
		return nil, "Price PnL history is available for open positions."
	}
	leg1, leg2 := executor.MonitoredPriceLegs(fills)
	if leg1 == nil || leg2 == nil {
		return nil, "Recorded opening fills are unavailable."
	}
	for _, leg := range []*executor.LiveFill{leg1, leg2} {
		if !leg.Filled || leg.FilledAmount <= 0 || !finite(leg.FilledAmount) || leg.AvgFillPrice <= 0 || !finite(leg.AvgFillPrice) ||
			(leg.Side != "long" && leg.Side != "short") {
			return nil, "Recorded opening fills are unavailable."
		}
	}
	if leg1.Side == leg2.Side || leg1.Venue == leg2.Venue || !sameVenuePair(leg1.Venue, leg2.Venue, pos.VenueA, pos.VenueB) {
		return nil, "Recorded opening fills are unavailable."
	}
	openedAt, err := time.Parse(time.RFC3339Nano, pos.OpenedAt)
	if err != nil {
		return nil, "Position opening time is unavailable."
	}
	for _, leg := range []*executor.LiveFill{leg1, leg2} {
		filledAt, err := time.Parse(time.RFC3339Nano, leg.FilledAt)
		if err != nil {
			return nil, "Recorded fill times are unavailable."
		}
		if filledAt.After(openedAt) {
			openedAt = filledAt
		}
	}
	longVenue, shortVenue := leg1.Venue, leg2.Venue
	if leg1.Side == "short" {
		longVenue, shortVenue = shortVenue, longVenue
	}
	return &positionPricePnLModel{leg1: *leg1, leg2: *leg2, openedAt: openedAt, longVenue: longVenue, shortVenue: shortVenue}, ""
}

func (m *positionPricePnLModel) point(at time.Time, mark1, mark2 float64) positionPricePnLPoint {
	point := positionPricePnLPoint{T: at.UTC().Format(time.RFC3339Nano)}
	if mark1 <= 0 || mark2 <= 0 || !finite(mark1) || !finite(mark2) {
		return point
	}
	parts := executor.PricePnLAtMarks(m.leg1, m.leg2, mark1, mark2)
	if !finite(parts.Leg1) || !finite(parts.Leg2) || !finite(parts.Total) {
		return point
	}
	long, short := parts.Leg1, parts.Leg2
	if m.leg1.Side == "short" {
		long, short = short, long
	}
	point.LongPnL, point.ShortPnL, point.PricePnL = &long, &short, &parts.Total
	return point
}

func buildPositionPricePnLContext(pos *executor.LivePosition, fills []executor.LiveFill) *positionPricePnLContext {
	model, reason := newPositionPricePnLModel(pos, fills)
	if model == nil {
		return &positionPricePnLContext{Reason: reason}
	}
	at, err := time.Parse(time.RFC3339Nano, pos.MonitorAt)
	point := model.point(at, pos.Leg1CurPrice, pos.Leg2CurPrice)
	if err != nil || at.Before(model.openedAt) || point.PricePnL == nil || *point.PricePnL != pos.PricePnL {
		return &positionPricePnLContext{Reason: "Waiting for an updated Price PnL snapshot."}
	}
	return &positionPricePnLContext{Available: true, LongVenue: model.longVenue, ShortVenue: model.shortVenue,
		OpenedAt: model.openedAt.UTC().Format(time.RFC3339Nano), Latest: &point, ValuationKey: model.key()}
}

func (s *Server) handlePositionPricePnLHistory(w http.ResponseWriter, r *http.Request) {
	bindings, ok := liveAccountPairFromQuery(w, r)
	if !ok {
		return
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/live/positions/"), "/price-pnl")
	pos, err := s.liveStore.GetPositionForBindings(r.Context(), id, bindings)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "position not found"})
		return
	}
	fills, err := s.liveStore.GetFills(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load opening fills"})
		return
	}
	model, reason := newPositionPricePnLModel(pos, fills)
	if model == nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": reason})
		return
	}
	params := historyLoadParams{asset: pos.Asset, venueA: pos.VenueA, venueB: pos.VenueB,
		rangeStr: "24h", priceSpread: true, spec: rangeSpec["24h"]}
	params.spec.source = sourceRaw
	history, _, err := s.cachedHistory(r.Context(), params)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load Price PnL history"})
		return
	}
	points := make([]positionPricePnLPoint, 0, len(history.Points))
	for _, sample := range history.Points {
		at, err := time.Parse(time.RFC3339Nano, sample.T)
		if err != nil || at.Before(model.openedAt) {
			continue
		}
		mark1, mark2 := sample.MarkPriceA, sample.MarkPriceB
		if !strings.EqualFold(model.leg1.Venue, pos.VenueA) {
			mark1, mark2 = mark2, mark1
		}
		point := positionPricePnLPoint{T: sample.T}
		if mark1 != nil && mark2 != nil {
			point = model.point(at, *mark1, *mark2)
		}
		point.Segment = &sample.Segment
		points = append(points, point)
	}
	now := time.Now().UTC()
	writeJSON(w, http.StatusOK, map[string]any{"position_id": pos.ID, "valuation_key": model.key(), "points": points,
		"start": now.Add(-24 * time.Hour).Format(time.RFC3339Nano), "end": now.Format(time.RFC3339Nano)})
}
