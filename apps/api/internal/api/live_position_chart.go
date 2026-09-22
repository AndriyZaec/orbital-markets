package api

import (
	"encoding/json"
	"math"
	"strings"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/executor"
)

const (
	chartContextFillsUnavailable      = "trustworthy long/short fill evidence is unavailable"
	chartContextProjectionUnavailable = "position projection inputs are unavailable"
)

type positionChartContext struct {
	Available         bool    `json:"available"`
	UnavailableReason string  `json:"unavailable_reason,omitempty"`
	Asset             string  `json:"asset,omitempty"`
	VenueA            string  `json:"venue_a,omitempty"`
	VenueB            string  `json:"venue_b,omitempty"`
	Direction         string  `json:"direction,omitempty"`
	CurrentAPR        float64 `json:"current_apr"`
	Notional          float64 `json:"notional"`
	FeeEstimate       float64 `json:"fee_estimate"`
	SlippageEstimate  float64 `json:"slippage_estimate"`
	ProjectionSource  string  `json:"projection_source,omitempty"`
	ProjectionWarning string  `json:"projection_warning,omitempty"`
}

type persistedChartPlan struct {
	Plan *struct {
		ID    string `json:"id"`
		Asset string `json:"asset"`
		Leg1  struct {
			Venue    string   `json:"venue"`
			Fee      *float64 `json:"fee"`
			Slippage *float64 `json:"slippage"`
		} `json:"leg_1"`
		Leg2 struct {
			Venue    string   `json:"venue"`
			Fee      *float64 `json:"fee"`
			Slippage *float64 `json:"slippage"`
		} `json:"leg_2"`
	} `json:"plan"`
}

func buildPositionChartContext(position *executor.LivePosition, fills []executor.LiveFill, sessionPayload []byte) positionChartContext {
	context := positionChartContext{Available: false, UnavailableReason: chartContextFillsUnavailable}
	if position == nil || position.Notional <= 0 || !finite(position.Notional) || !finite(position.CurrentSpread) {
		context.UnavailableReason = chartContextProjectionUnavailable
		return context
	}

	sides := map[string]string{}
	fillFees := 0.0
	for _, fill := range fills {
		if !fill.Filled || fill.FilledAmount <= 0 {
			continue
		}
		venue := strings.ToLower(fill.Venue)
		if venue != strings.ToLower(position.VenueA) && venue != strings.ToLower(position.VenueB) {
			return context
		}
		side := strings.ToLower(fill.Side)
		if side != "long" && side != "short" {
			return context
		}
		if previous, ok := sides[venue]; ok && previous != side {
			return context
		}
		sides[venue] = side
		if finite(fill.Fee) {
			fillFees += math.Abs(fill.Fee)
		}
	}

	venueA := strings.ToLower(position.VenueA)
	venueB := strings.ToLower(position.VenueB)
	var direction string
	switch {
	case venueA != venueB && sides[venueA] == "long" && sides[venueB] == "short":
		direction = "long_a_short_b"
	case venueA != venueB && sides[venueA] == "short" && sides[venueB] == "long":
		direction = "long_b_short_a"
	default:
		return context
	}

	context = positionChartContext{
		Available: true, Asset: position.Asset, VenueA: position.VenueA, VenueB: position.VenueB,
		Direction: direction, CurrentAPR: position.CurrentSpread, Notional: position.Notional,
		FeeEstimate: fillFees / position.Notional, ProjectionSource: "fills_fallback",
		ProjectionWarning: "opening slippage assumptions are unavailable; projection includes recorded fill fees only",
	}

	var persisted persistedChartPlan
	if json.Unmarshal(sessionPayload, &persisted) != nil || persisted.Plan == nil {
		return context
	}
	plan := persisted.Plan
	if plan.ID != position.PlanID || !strings.EqualFold(plan.Asset, position.Asset) ||
		!sameVenuePair(plan.Leg1.Venue, plan.Leg2.Venue, position.VenueA, position.VenueB) ||
		plan.Leg1.Fee == nil || plan.Leg2.Fee == nil || plan.Leg1.Slippage == nil || plan.Leg2.Slippage == nil {
		return context
	}
	values := []float64{*plan.Leg1.Fee, *plan.Leg2.Fee, *plan.Leg1.Slippage, *plan.Leg2.Slippage}
	for _, value := range values {
		if value < 0 || !finite(value) {
			return context
		}
	}
	context.FeeEstimate = *plan.Leg1.Fee + *plan.Leg2.Fee
	context.SlippageEstimate = *plan.Leg1.Slippage + *plan.Leg2.Slippage
	context.ProjectionSource = "execution_plan"
	context.ProjectionWarning = ""
	return context
}

func sameVenuePair(a, b, expectedA, expectedB string) bool {
	return (strings.EqualFold(a, expectedA) && strings.EqualFold(b, expectedB)) ||
		(strings.EqualFold(a, expectedB) && strings.EqualFold(b, expectedA))
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
