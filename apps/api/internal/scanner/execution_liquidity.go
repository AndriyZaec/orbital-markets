package scanner

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/liquidity"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
)

type ExecutionLiquiditySource interface {
	Quote(context.Context, liquidity.Pair, liquidity.OrderSize, bool) (liquidity.OrderQuote, error)
}

var ErrFreshBBOUnavailable = errors.New("fresh BBO depth unavailable; checking again automatically")

type AmountNormalizer func(float64, ...domain.Leg) (float64, error)

func (s *Scanner) EnableAmountNormalization(normalize AmountNormalizer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.normalizeAmount = normalize
}

func freshBookTime(market venue.MarketData, now time.Time) (time.Time, bool) {
	at := market.Timestamp
	if market.Health != nil {
		if market.Health.Book.Status != venue.HealthAvailable {
			return time.Time{}, false
		}
		at = market.Health.Book.SourceTime
		if at.IsZero() {
			at = market.Health.Book.ReceivedAt
		}
	}
	return at, !at.IsZero() && !at.After(now.Add(time.Second)) && now.Sub(at) <= 5*time.Second
}

func (s *Scanner) EnableExecutionLiquidity(source ExecutionLiquiditySource) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.executionLiquidity = source
}

// EvaluateExecutionLiquidity uses the actual common base amount, not two
// independently sized quote notionals. No impact threshold makes it ineligible.
func (s *Scanner) EvaluateExecutionLiquidity(ctx context.Context, plan *domain.ExecutionPlan, size liquidity.OrderSize, fresh bool) error {
	if !((positiveFinite(size.Notional) && size.BaseAmount == 0) || (positiveFinite(size.BaseAmount) && size.Notional == 0)) {
		return fmt.Errorf("invalid order amount")
	}
	long, short, err := s.FreshSnapshots(ctx, plan.Asset, plan.Leg1.Venue, plan.Leg2.Venue)
	if err != nil {
		return err
	}
	if rejection := validatePairing(plan.Asset, long, short, time.Now()); rejection != nil {
		return rejection
	}
	if plan.Leg1.Side != domain.SideLong {
		long, short = short, long
	}
	if long.FundingRate > short.FundingRate {
		return fmt.Errorf("funding direction changed; refresh opportunity")
	}
	amount := size.BaseAmount
	if size.Notional > 0 {
		amount = size.Notional / max(long.AskPrice, short.BidPrice)
	}
	s.mu.RLock()
	source := s.executionLiquidity
	s.mu.RUnlock()
	quote := liquidity.OrderQuote{}
	quoteErr := liquidity.ErrUnavailable
	now := time.Now()
	longAt, longFresh := freshBookTime(long, now)
	shortAt, shortFresh := freshBookTime(short, now)
	coveredByBBO := positiveFinite(long.AskPrice) && positiveFinite(short.BidPrice) &&
		positiveFinite(long.AskSize) && positiveFinite(short.BidSize) &&
		amount <= long.AskSize/long.AskPrice && amount <= short.BidSize/short.BidPrice
	result := &domain.ExecutionLiquidity{Status: "unavailable"}
	plan.Liquidity = result
	if coveredByBBO {
		if !longFresh || !shortFresh {
			return ErrFreshBBOUnavailable
		}
		result.Status = "bbo"
		result.ObservedAt = minSnapshotTime(longAt, shortAt)
		quote.BaseAmount = amount
		quote.LongPrice, quote.ShortPrice = long.AskPrice, short.BidPrice
		quote.LongWorstPrice, quote.ShortWorstPrice = long.AskPrice, short.BidPrice
	} else if source != nil {
		quote, quoteErr = source.Quote(ctx, liquidity.Pair{
			Long:  liquidity.Market{Venue: long.Venue, Key: long.MarketKey},
			Short: liquidity.Market{Venue: short.Venue, Key: short.MarketKey},
		}, liquidity.OrderSize{BaseAmount: amount}, fresh)
	}
	if !coveredByBBO && quoteErr == nil {
		if !positiveFinite(quote.BaseAmount) {
			return fmt.Errorf("invalid quoted order amount")
		}
		amount = quote.BaseAmount
		result.ObservedAt = quote.ObservedAt
		if !quote.Sufficient {
			result.Status = "insufficient"
			return fmt.Errorf("insufficient visible order-book depth for the full position on both venues")
		}
		result.Status = "l2"
		result.Impact = quote.LongImpact + quote.ShortImpact
		result.RequiresAcknowledgement = result.Impact > liquidity.PairImpactBudget+1e-12
	} else if !coveredByBBO {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("depth verification unavailable for this size; checking again automatically")
	}
	for _, leg := range []*domain.Leg{&plan.Leg1, &plan.Leg2} {
		impact, worst, price := quote.LongImpact, quote.LongWorstPrice, quote.LongPrice
		multiplier := 1 + plan.Bounds.MaxSlippagePct
		if leg.Side == domain.SideShort {
			impact, worst, price = quote.ShortImpact, quote.ShortWorstPrice, quote.ShortPrice
			multiplier = 1 - plan.Bounds.MaxSlippagePct
		}
		// Keep the BBO spread estimate, adding only the measured depth component.
		leg.Slippage = max(0, leg.Slippage-leg.DepthImpact) + impact
		leg.DepthImpact = impact
		leg.ExpectedPrice, leg.LimitPrice = price, worst*multiplier
		averagePrice := price * (1 + impact)
		if leg.Side == domain.SideShort {
			averagePrice = price * (1 - impact)
		}
		leg.MarginRequired = amount * averagePrice / leg.Leverage
		leg.LiquidationPrice = domain.LiquidationPrice(averagePrice, leg.Side, leg.Leverage)
		leg.LiquidationDistance = domain.LiquidationDistance(averagePrice, leg.LiquidationPrice, leg.Side)
		leg.LiquidationRisk = domain.ClassifyLiqRisk(leg.LiquidationDistance, leg.LiquidationPrice)
	}
	plan.BaseAmount = amount
	plan.Leverage.MarginRequired = plan.Leg1.MarginRequired + plan.Leg2.MarginRequired
	plan.Leverage.GrossExposure = plan.Leg1.MarginRequired*plan.Leg1.Leverage + plan.Leg2.MarginRequired*plan.Leg2.Leverage
	plan.Leverage.EffectiveLeverage = plan.Leverage.GrossExposure / plan.Leverage.MarginRequired
	plan.ExpectedSpread = (plan.Leg2.ExpectedPrice - plan.Leg1.ExpectedPrice) / plan.Leg1.ExpectedPrice
	plan.EstimatedNetEdge = domain.EstimatedNetEdge(domain.AnnualizedGrossEdge(short.FundingRate, long.FundingRate), plan.Leg1.Slippage+plan.Leg1.Fee+plan.Leg2.Slippage+plan.Leg2.Fee)
	return nil
}

func minSnapshotTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// ConstrainExecutionPrices keeps a freshly evaluated quote inside the preview's
// approved limits without another depth read. The quote's marginal prices must
// still fit; favorable movement may tighten the limits but never widen them.
func ConstrainExecutionPrices(plan *domain.ExecutionPlan, approved map[string]float64) error {
	if approved == nil {
		return nil // Legacy callers have no preview-bound price limits.
	}
	if len(approved) != 2 {
		return fmt.Errorf("approved price limits required for both plan venues")
	}
	for _, leg := range []*domain.Leg{&plan.Leg1, &plan.Leg2} {
		limit, ok := approved[leg.Venue]
		if !ok || !positiveFinite(limit) {
			return fmt.Errorf("invalid approved price limit for %s", leg.Venue)
		}
		multiplier := 1 + plan.Bounds.MaxSlippagePct
		constrained := min(leg.LimitPrice, limit)
		if leg.Side == domain.SideShort {
			multiplier = 1 - plan.Bounds.MaxSlippagePct
			constrained = max(leg.LimitPrice, limit)
		}
		worst := leg.LimitPrice / multiplier
		if outsideExecutionPriceLimit(worst, constrained, leg.Side) {
			return fmt.Errorf("market moved outside the approved price for %s; refresh quote and try again", leg.Venue)
		}
		leg.LimitPrice = constrained
	}
	return nil
}

func outsideExecutionPriceLimit(price, limit float64, side domain.Side) bool {
	// Allow representation noise only, never another quote-movement allowance.
	epsilon := max(price, limit) * (math.Nextafter(1, 2) - 1) * 8
	return (side == domain.SideLong && price-limit > epsilon) ||
		(side == domain.SideShort && limit-price > epsilon)
}

// ValidateExecutionLiquidity verifies actual depth without mutating or widening
// the signed plan. Higher impact needs the previously supplied acknowledgement.
func (s *Scanner) ValidateExecutionLiquidity(ctx context.Context, approved *domain.ExecutionPlan, amount float64) error {
	check := *approved
	if err := s.EvaluateExecutionLiquidity(ctx, &check, liquidity.OrderSize{BaseAmount: amount}, true); err != nil {
		return err
	}
	if err := check.AcknowledgeImpact(approved.ImpactAcknowledged); err != nil {
		return err
	}
	for i, leg := range []domain.Leg{check.Leg1, check.Leg2} {
		limit := []domain.Leg{approved.Leg1, approved.Leg2}[i].LimitPrice
		if limit <= 0 {
			continue
		}
		worst := leg.LimitPrice / (1 + check.Bounds.MaxSlippagePct)
		if leg.Side == domain.SideShort {
			worst = leg.LimitPrice / (1 - check.Bounds.MaxSlippagePct)
		}
		if outsideExecutionPriceLimit(worst, limit, leg.Side) {
			return fmt.Errorf("insufficient depth within the approved order price; quote is updating automatically")
		}
	}
	return nil
}
