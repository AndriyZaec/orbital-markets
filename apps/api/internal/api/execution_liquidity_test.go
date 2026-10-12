package api

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/liquidity"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/scanner"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
)

type liveDepthStub struct {
	quote  liquidity.OrderQuote
	err    error
	fresh  bool
	amount float64
}

type executionQuoteAdapter struct {
	name string
	data venue.MarketData
}

func (a executionQuoteAdapter) Name() string { return a.name }
func (a executionQuoteAdapter) FetchMarketData(context.Context) ([]venue.MarketData, error) {
	return []venue.MarketData{a.data}, nil
}

func (d *liveDepthStub) Quote(_ context.Context, _ liquidity.Pair, size liquidity.OrderSize, fresh bool) (liquidity.OrderQuote, error) {
	amount := size.BaseAmount
	if size.Notional > 0 {
		amount = size.Notional / max(d.quote.LongPrice, d.quote.ShortPrice)
	}
	d.fresh, d.amount = fresh, amount
	quote := d.quote
	quote.BaseAmount = amount
	return quote, d.err
}

func TestOpeningDepthIsRevalidatedForTheActualAmountAndAcknowledgement(t *testing.T) {
	now := time.Now()
	adapter := func(name, key string, funding float64) executionQuoteAdapter {
		return executionQuoteAdapter{name, venue.MarketData{Venue: name, Asset: "BTC", MarketKey: key, MarkPrice: 100, IndexPrice: 100,
			BidPrice: 99.99, AskPrice: 100.01, BidSize: 100, AskSize: 100, OpenInterest: 100000, MaxLeverage: 10, FundingRate: funding, Timestamp: now}}
	}
	sc := scanner.New(slog.Default(), adapter("aster", "BTCUSDT", 0.001), adapter("hyperliquid", "BTC", 0.002))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sc.Run(ctx, time.Hour)
	deadline := time.Now().Add(time.Second)
	for len(sc.Opportunities()) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(sc.Opportunities()) == 0 {
		t.Fatal("no opportunity")
	}
	source := &liveDepthStub{quote: liquidity.OrderQuote{Sufficient: true, LongPrice: 100.01, ShortPrice: 99.99,
		LongImpact: 0.006, ShortImpact: 0.004, LongWorstPrice: 101, ShortWorstPrice: 99, ObservedAt: now}}
	sc.EnableExecutionLiquidity(source)
	plan, err := sc.BuildPlan(ctx, sc.Opportunities()[0].ID, 2, 500)
	if err != nil || !plan.Executable {
		t.Fatalf("plan: %+v, %v", plan, err)
	}
	if err := plan.AcknowledgeImpact(false); err == nil {
		t.Fatal("unacknowledged impact accepted")
	}
	if err := plan.AcknowledgeImpact(true); err != nil {
		t.Fatal(err)
	}
	leg1, leg2 := orderLegsByRisk(plan)
	session := &LiveSession{Plan: plan, Leg1: leg1, Leg2: leg2, Leg1OpenReq: &domain.SigningRequest{Amount: 3}}
	s := &Server{scanner: sc}
	if err := s.validateFreshOpenDepth(ctx, session); err != nil || !source.fresh || source.amount != 3 {
		t.Fatalf("actual normalized amount not checked: %v, %+v", err, source)
	}
	source.quote.Sufficient = false
	if err := s.validateFreshOpenDepth(ctx, session); err == nil {
		t.Fatal("book shrank after signing, but opening was allowed")
	}
	source.quote.Sufficient = true
	source.quote.LongWorstPrice = 110
	if err := s.validateFreshOpenDepth(ctx, session); err == nil {
		t.Fatal("opening allowed without sufficient depth within signed prices")
	}
	source.quote.LongWorstPrice = 101
	plan.ImpactAcknowledged = false
	if err := s.validateFreshOpenDepth(ctx, session); err == nil {
		t.Fatal("new high impact bypassed acknowledgement")
	}
	source.err = liquidity.ErrUnavailable
	if err := s.validateFreshOpenDepth(ctx, session); err == nil {
		t.Fatal("unknown beyond-BBO depth accepted")
	}
	session.Leg1OpenReq.Amount = 0.5
	if err := s.validateFreshOpenDepth(ctx, session); err != nil {
		t.Fatalf("fresh BBO fallback blocked: %v", err)
	}
}
