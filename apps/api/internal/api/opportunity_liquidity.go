package api

import (
	"context"
	"net/http"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/liquidity"
)

type liquidityQuoteSource interface {
	Estimate(context.Context, liquidity.Pair) (liquidity.Estimate, error)
}

func (s *Server) EnableOpportunityLiquidity(source liquidityQuoteSource) {
	s.opportunityLiquidity = source
}

func (s *Server) handleOpportunityLiquidity(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	unavailable := func() { writeJSON(w, http.StatusOK, map[string]bool{"available": false}) }
	if s.opportunityLiquidity == nil || s.scanner == nil {
		unavailable()
		return
	}
	var selected *domain.Opportunity
	for _, opp := range s.scanner.Opportunities() {
		if opp.ID == r.PathValue("opportunityID") && opp.Status == domain.OpportunityAvailable {
			selected = &opp
			break
		}
	}
	if selected == nil {
		unavailable()
		return
	}
	a, err := s.scanner.MarketSnapshot(r.Context(), selected.VenuePair.VenueA, selected.Asset)
	if err != nil {
		unavailable()
		return
	}
	b, err := s.scanner.MarketSnapshot(r.Context(), selected.VenuePair.VenueB, selected.Asset)
	if err != nil {
		unavailable()
		return
	}
	pair := liquidity.Pair{Long: liquidity.Market{Venue: a.Venue, Key: a.MarketKey}, Short: liquidity.Market{Venue: b.Venue, Key: b.MarketKey}}
	if selected.Direction == domain.DirectionLongB {
		pair.Long, pair.Short = pair.Short, pair.Long
	}
	quote, err := s.opportunityLiquidity.Estimate(r.Context(), pair)
	if err != nil {
		unavailable()
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"available": true, "capacity": quote.Notional, "impact_budget": liquidity.PairImpactBudget,
		"book_limited": quote.BookLimited, "observed_at": quote.ObservedAt,
		"direction": selected.Direction,
	})
}
