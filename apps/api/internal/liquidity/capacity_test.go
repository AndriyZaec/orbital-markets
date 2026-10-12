package liquidity

import (
	"math"
	"testing"
	"time"
)

func testBook(now time.Time, bids, asks []level) book { return book{bids: bids, asks: asks, at: now} }

func TestCapacityUsesCommonQuantityAndPartialLastLevel(t *testing.T) {
	now := time.Now()
	long := testBook(now, []level{{99, 5}}, []level{{100, 1}, {110, 1}})
	short := testBook(now, []level{{100, 1}, {90, 1}}, []level{{101, 5}})
	result, ok := capacity(long, short, now)
	// Each leg has 10% worse prices at level two. The combined VWAP impact
	// reaches 0.2% at q=1/(1-0.002/0.2), not at independent quote quantities.
	wantQuantity := 1 / (1 - PairImpactBudget/0.2)
	if !ok || result.BookLimited || math.Abs(result.Notional-wantQuantity*100) > 1e-8 {
		t.Fatalf("capacity = %+v, %t; want %v", result, ok, wantQuantity*100)
	}
	if math.Abs(vwap(long.asks, wantQuantity)/100-1+1-vwap(short.bids, wantQuantity)/100-PairImpactBudget) > 1e-10 {
		t.Fatal("combined impact does not match budget")
	}
}

func TestCapacityStopsAtVisibleDepthEvenBelowBudget(t *testing.T) {
	now := time.Now()
	long := testBook(now, []level{{99, 10}}, []level{{100, 2}})
	short := testBook(now, []level{{100, 8}, {99.99, 20}}, []level{{101, 30}})
	result, ok := capacity(long, short, now)
	if !ok || !result.BookLimited || result.Notional != 200 {
		t.Fatalf("capacity = %+v, %t, want visible long limit $200", result, ok)
	}
	if !math.IsInf(vwap(long.asks, 3), 1) {
		t.Fatal("VWAP invented depth")
	}
}

func TestCapacityExcludesSpreadFromDepthImpact(t *testing.T) {
	now := time.Now()
	// Wide bid/ask spreads are owned by existing execution checks, not this
	// display-only depth budget. With one level there is no added depth impact.
	long := testBook(now, []level{{80, 10}}, []level{{100, 5}})
	short := testBook(now, []level{{100, 5}}, []level{{120, 10}})
	result, ok := capacity(long, short, now)
	if !ok || !result.BookLimited || result.Notional != 500 {
		t.Fatalf("spread incorrectly consumed depth budget: %+v", result)
	}
}

func TestCapacityRejectsUnusableBooks(t *testing.T) {
	now := time.Now()
	valid := testBook(now, []level{{99, 2}, {98, 2}}, []level{{100, 2}, {101, 2}})
	tests := map[string]book{
		"empty":         {},
		"stale":         testBook(now.Add(-maxBookAge-time.Nanosecond), valid.bids, valid.asks),
		"future":        testBook(now.Add(2*time.Second), valid.bids, valid.asks),
		"skew":          testBook(now.Add(-maxBookSkew-time.Nanosecond), valid.bids, valid.asks),
		"unordered":     testBook(now, []level{{98, 2}, {99, 2}}, valid.asks),
		"duplicate":     testBook(now, []level{{99, 2}, {99, 3}}, valid.asks),
		"crossed":       testBook(now, []level{{101, 2}}, []level{{100, 2}}),
		"nan":           testBook(now, valid.bids, []level{{math.NaN(), 1}}),
		"zero quantity": testBook(now, valid.bids, []level{{100, 0}}),
	}
	for name, invalid := range tests {
		t.Run(name, func(t *testing.T) {
			if _, ok := capacity(valid, invalid, now); ok {
				t.Fatal("unusable book accepted")
			}
		})
	}
}

func TestOrderQuoteAllowsHighImpactButRequiresFullVisibleDepth(t *testing.T) {
	now := time.Now()
	long := testBook(now, []level{{99, 5}}, []level{{100, 1}, {110, 1}})
	short := testBook(now, []level{{100, 1}, {90, 1}}, []level{{101, 5}})
	quote, err := quoteOrder(long, short, 2, now)
	if err != nil || !quote.Sufficient || math.Abs(quote.LongImpact+quote.ShortImpact-0.1) > 1e-12 || quote.LongWorstPrice != 110 || quote.ShortWorstPrice != 90 {
		t.Fatalf("full high-impact order was rejected or mispriced: %+v, %v", quote, err)
	}
	partial, err := quoteOrder(long, short, 1.5, now)
	if err != nil || math.Abs(partial.LongImpact-0.1/3) > 1e-12 {
		t.Fatalf("partial level: %+v, %v", partial, err)
	}
	quote, err = quoteOrder(long, short, 2.01, now)
	if err != nil || quote.Sufficient {
		t.Fatalf("invented depth: %+v, %v", quote, err)
	}
	if _, err := quoteOrder(long, short, 0, now); err == nil {
		t.Fatal("invalid amount accepted")
	}
	short.at = now.Add(-6 * time.Second)
	if _, err := quoteOrder(long, short, 1, now); err == nil {
		t.Fatal("stale execution book accepted")
	}
}
