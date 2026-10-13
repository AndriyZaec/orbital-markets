package api

import (
	"context"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/liquidity"
)

type crossingPrepareDepth struct {
	initialLong, initialShort float64
	finalLong, finalShort     float64
}

func (d crossingPrepareDepth) Quote(_ context.Context, _ liquidity.Pair, size liquidity.OrderSize, fresh bool) (liquidity.OrderQuote, error) {
	quote := liquidity.OrderQuote{BaseAmount: size.BaseAmount, Sufficient: true, LongPrice: 100, ShortPrice: 100,
		LongWorstPrice: 101, ShortWorstPrice: 99, LongImpact: d.initialLong, ShortImpact: d.initialShort, ObservedAt: time.Now()}
	if fresh {
		quote.LongImpact, quote.ShortImpact = d.finalLong, d.finalShort
	}
	return quote, nil
}

func TestPrepareOrdersLegsUsingFinalDepthAndPreservesBaselines(t *testing.T) {
	for _, test := range []struct {
		name    string
		depth   crossingPrepareDepth
		riskier string
	}{
		{"short becomes riskier", crossingPrepareDepth{0.003, 0.001, 0.001, 0.003}, "hyperliquid"},
		{"long becomes riskier", crossingPrepareDepth{0.001, 0.003, 0.003, 0.001}, "pacifica"},
		{"tie retains plan order", crossingPrepareDepth{0.001, 0.003, 0.002, 0.002}, "pacifica"},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newLocalPrepareFixture(t)
			f.body["requested_notional"], f.body["impact_acknowledged"] = 500, true
			f.s.scanner.EnableExecutionLiquidity(test.depth)
			// Dust below the existing position gate makes a baseline swap observable.
			baselines := map[string]float64{"pacifica": 5e-10, "hyperliquid": 4e-10}
			for venueName, amount := range baselines {
				feed := f.feeds[venueName]
				feed.mu.Lock()
				feed.snapshot.Positions = []liveAccountPosition{{Symbol: "BTC", Side: "long", Size: amount}}
				feed.mu.Unlock()
			}
			status, result, body, _ := f.prepare(t, context.Background(), true)
			if status != 200 {
				t.Fatalf("prepare: %d %s", status, body)
			}
			if result.RiskierVenue != test.riskier {
				t.Fatalf("riskier=%s want=%s", result.RiskierVenue, test.riskier)
			}
			session, found, claimed := f.s.live.sessions.claimForAccounts(result.SessionID, f.body["accounts"].(map[string]string))
			if !found || !claimed {
				t.Fatal("session not found")
			}
			defer f.s.live.sessions.release(result.SessionID)
			if session.BaselineLeg1Size != baselines[session.Leg1.venue] || session.BaselineLeg2Size != baselines[session.Leg2.venue] {
				t.Fatal("final leg order lost its corresponding account baselines")
			}
			if session.Leg1OpenReq.Amount != 5 {
				t.Fatal("reordering changed the normalized hedge quantity")
			}
			for _, request := range result.SigningRequests {
				if (request.Action == "open" || request.Action == "unwind") && request.Venue != test.riskier {
					t.Fatal("open and safety unwind do not match the final riskier venue")
				}
			}
		})
	}
}
