package api

import (
	"context"
	"math"
	"math/big"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/executor"
)

// Overlay pending funding only on a live response, not on the persisted cash
// ledger or its rate-based fallback. A new response always starts from the DB.
func (s *Server) addPhoenixPendingFunding(ctx context.Context, position *executor.LivePosition, fills []executor.LiveFill) {
	if position.VenueA != "phoenix" && position.VenueB != "phoenix" {
		return
	}
	if position.State != "open" && position.State != "degraded" && position.State != "closing" {
		return
	}
	view := &executor.PendingFundingPnL{UnavailableReason: "Settled funding reconciliation required"}
	position.PhoenixPendingFunding = view
	if position.FundingPnLSource != "realized" {
		return // The estimate may already include this pending interval.
	}
	view.UnavailableReason = "Phoenix account snapshot unavailable"
	owner := position.AccountBindings["phoenix"]
	if owner == "" || s.live == nil || s.live.accounts == nil {
		return
	}
	lease, ok := s.live.accounts.LookupPassive("phoenix", owner)
	if !ok {
		return
	}
	defer lease.Release()
	snapshot := lease.Feed().Snapshot()
	now := time.Now()
	if snapshot.Venue != "phoenix" || snapshot.Account != owner || !snapshot.Connected || snapshot.UnavailableReason != "" ||
		!phoenixTimeFresh(snapshot.PositionsUpdatedAt, now, admissionFreshness) || !phoenixTimeFresh(snapshot.LastUpdated, now, admissionFreshness) {
		view.UnavailableReason = "Phoenix account snapshot is not fresh and synchronized"
		return
	}
	latest, err := s.liveStore.LatestFundingPaymentAt(ctx, position.ID, "phoenix")
	if err != nil || latest.After(snapshot.PositionsUpdatedAt) {
		view.UnavailableReason = "Phoenix account snapshot must catch up with settled funding"
		return
	}
	if fills == nil {
		fills, err = s.liveStore.GetFills(ctx, position.ID)
		if err != nil {
			view.UnavailableReason = "Phoenix strategy fill unavailable"
			return
		}
	}
	var leg *executor.LiveFill
	for i := range fills {
		if fills[i].Venue == "phoenix" && fills[i].Filled && fills[i].FilledAmount > 0 {
			if leg != nil {
				view.UnavailableReason = "Phoenix strategy exposure is ambiguous"
				return
			}
			leg = &fills[i]
		}
	}
	view.UnavailableReason = "Phoenix account exposure does not match the strategy leg"
	if leg == nil {
		return
	}
	for _, accountPosition := range snapshot.Positions {
		if accountPosition.Symbol != leg.Symbol {
			continue
		}
		if accountPosition.Side != leg.Side || math.IsNaN(accountPosition.Size) || math.IsInf(accountPosition.Size, 0) ||
			math.Abs(accountPosition.Size-leg.FilledAmount) > math.Abs(leg.FilledAmount)*1e-12 {
			return
		}
		lots, ok := new(big.Int).SetString(accountPosition.PendingFundingQuoteLots, 10)
		if !ok {
			view.UnavailableReason = "Phoenix pending funding amount unavailable"
			return
		}
		amount, _ := new(big.Rat).SetFrac(lots, big.NewInt(1_000_000)).Float64()
		if math.IsNaN(amount) || math.IsInf(amount, 0) {
			view.UnavailableReason = "Phoenix pending funding amount unavailable"
			return
		}
		view.AmountUSD = &amount
		view.AsOf = &snapshot.PositionsUpdatedAt
		view.UnavailableReason = ""
		position.TotalPnL += amount
		return
	}
}
