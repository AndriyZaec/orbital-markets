package phoenix

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
)

func TestAdapterPublishesPhoenixFundingAsHourlyFraction(t *testing.T) {
	// MON/BTC values captured from public marketStatsV2 on 2026-11-09.
	// Phoenix reports percentage points, including annualizedFundingRate.
	for _, tc := range []struct {
		name, symbol, message string
		interval              time.Duration
		wantHourlyFraction    float64
		wantAnnualPercent     float64
	}{
		{"negative MON", "MON", `{"channel":"marketStatsV2","stats":[{"symbol":"MON","timestamp":1794249280,"openInterest":2876732,"markPrice":0.016628,"oraclePrice":0.016655,"currentFundingRate":-0.008554847245609813,"annualizedFundingRate":-74.94046187154196}]}`, time.Hour, -0.00008554847245609813, -74.94046187154196},
		{"positive BTC", "BTC", `{"channel":"marketStatsV2","stats":[{"symbol":"BTC","timestamp":1794249280,"openInterest":18.4688,"markPrice":74582,"oraclePrice":74574,"currentFundingRate":0.0012526145718806145,"annualizedFundingRate":10.972903649674183}]}`, time.Hour, 0.000012526145718806145, 10.972903649674183},
		{"zero", "MON", `{"channel":"marketStatsV2","stats":[{"symbol":"MON","timestamp":1794249280,"openInterest":1,"markPrice":100,"oraclePrice":100,"currentFundingRate":0}]}`, time.Hour, 0, 0},
		{"two-hour interval", "MON", `{"channel":"marketStatsV2","stats":[{"symbol":"MON","timestamp":1794249280,"openInterest":1,"markPrice":100,"oraclePrice":100,"currentFundingRate":0.02}]}`, 2 * time.Hour, 0.0001, 87.6},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Unix(1794249281, 0).UTC()
			stream := NewMarketStream(nil, discardLogger())
			stream.now = func() time.Time { return now }
			stream.replaceMetadata(ExchangeMetadata{Markets: []MarketMetadata{{Symbol: tc.symbol, Asset: tc.symbol, Status: "active", FundingInterval: tc.interval}}})
			generation := stream.markConnected()
			stream.haveExchangeSnapshot = true
			if err := stream.applyMessage(generation, []byte(tc.message)); err != nil {
				t.Fatal(err)
			}
			if err := stream.applyOrderbook(generation, orderbookEnvelope{Symbol: tc.symbol, Orderbook: orderbookWire{Bids: [][]float64{{99, 2}}, Asks: [][]float64{{101, 2}}}}); err != nil {
				t.Fatal(err)
			}
			adapter := &Adapter{stream: stream, logger: discardLogger(), now: func() time.Time { return now }}
			markets, err := adapter.FetchMarketData(context.Background())
			if err != nil || len(markets) != 1 {
				t.Fatalf("markets = %+v, err = %v", markets, err)
			}
			rate := markets[0].FundingRate
			if math.Abs(rate-tc.wantHourlyFraction) > 1e-15 {
				t.Fatalf("hourly funding fraction = %.17g, want %.17g", rate, tc.wantHourlyFraction)
			}
			if got := domain.AnnualizeRate(rate) * 100; math.Abs(got-tc.wantAnnualPercent) > 1e-10 {
				t.Fatalf("annual funding = %.12g%%, want %.12g%%", got, tc.wantAnnualPercent)
			}
			stream.replaceMetadata(ExchangeMetadata{Markets: []MarketMetadata{{Symbol: tc.symbol, Asset: tc.symbol, Status: "active", FundingInterval: 2 * tc.interval}}})
			if got := stream.Snapshot().Stats[tc.symbol].HourlyFundingRate; math.Abs(got-tc.wantHourlyFraction/2) > 1e-15 {
				t.Fatalf("metadata refresh lost fractional units: hourly rate = %.17g", got)
			}
		})
	}
}

func TestAdapterPublishesNormalizedActiveMarket(t *testing.T) {
	now := time.Date(2026, time.October, 26, 12, 0, 10, 0, time.UTC)
	stream := NewMarketStream(nil, discardLogger())
	stream.now = func() time.Time { return now }
	stream.replaceMetadata(ExchangeMetadata{
		Accounts: ExchangeAccounts{PerpAssetMap: "rest-map", GlobalTraderIndex: []string{"index"}, ActiveTraderBuffer: []string{"buffer"}},
		Markets:  []MarketMetadata{{Symbol: "SOL", Asset: "SOL", Status: "active", FundingInterval: time.Hour, MaxLeverage: 20}},
	})
	generation := stream.markConnected()
	stream.mu.Lock()
	stream.haveExchangeSnapshot = true
	stream.mu.Unlock()
	if err := stream.applyMarketStats(generation, marketStatsV2Envelope{Stats: []marketStatsV2Wire{{
		Symbol: "SOL", Timestamp: now.Add(-2 * time.Second).Unix(), OpenInterest: 50,
		MarkPrice: 101, OraclePrice: 100, CurrentFundingRate: 0.02,
	}}}); err != nil {
		t.Fatal(err)
	}
	if err := stream.applyOrderbook(generation, orderbookEnvelope{
		Symbol: "SOL", Orderbook: orderbookWire{Bids: [][]float64{{99, 2}}, Asks: [][]float64{{102, 3}}},
	}); err != nil {
		t.Fatal(err)
	}
	adapter := &Adapter{stream: stream, logger: discardLogger(), now: func() time.Time { return now }}

	markets, err := adapter.FetchMarketData(context.Background())
	if err != nil || len(markets) != 1 {
		t.Fatalf("markets = %+v, err = %v", markets, err)
	}
	market := markets[0]
	if market.Venue != "phoenix" || market.Asset != "SOL" || market.MarketKey != "SOL" ||
		market.MarkPrice != 101 || market.IndexPrice != 100 || market.FundingRate != 0.0002 ||
		market.BidPrice != 99 || market.BidSize != 198 || market.AskPrice != 102 || market.AskSize != 306 ||
		market.OpenInterest != 50 || market.MaxLeverage != 20 || !market.Timestamp.Equal(now.Add(-2*time.Second)) {
		t.Fatalf("unexpected market: %+v", market)
	}
	if market.Health == nil || market.Health.Mark.Status != venue.HealthAvailable ||
		market.Health.Book.Status != venue.HealthAvailable || market.Health.REST.Status != venue.HealthAvailable ||
		market.Health.Stream.Status != venue.HealthAvailable {
		t.Fatalf("unexpected health: %+v", market.Health)
	}
	stream.markDisconnected(generation)
	markets, err = adapter.FetchMarketData(context.Background())
	if err != nil || len(markets) != 1 || markets[0].Health.Stream.Status != venue.HealthUnavailable ||
		markets[0].Health.Stream.Reason != venue.HealthReasonDisconnected {
		t.Fatalf("disconnected fallback = %+v, err = %v", markets, err)
	}
	nextGeneration := stream.markConnected()
	markets, err = adapter.FetchMarketData(context.Background())
	if err != nil || len(markets) != 1 || markets[0].Health.Stream.Reason != venue.HealthReasonIncompleteSnapshot {
		t.Fatalf("reconnecting fallback = %+v, err = %v", markets, err)
	}
	stream.mu.Lock()
	stream.haveExchangeSnapshot = true
	stream.mu.Unlock()
	if err := stream.applyMarketStats(nextGeneration, marketStatsV2Envelope{Stats: []marketStatsV2Wire{{
		Symbol: "SOL", Timestamp: now.Add(-time.Second).Unix(), OpenInterest: 50,
		MarkPrice: 101, OraclePrice: 100, CurrentFundingRate: 0.02,
	}}}); err != nil {
		t.Fatal(err)
	}
	markets, err = adapter.FetchMarketData(context.Background())
	if err != nil || len(markets) != 0 {
		t.Fatalf("synchronized stream reused prior-generation BBO: %+v, err = %v", markets, err)
	}
	if err := stream.applyOrderbook(nextGeneration, orderbookEnvelope{
		Symbol: "SOL", Orderbook: orderbookWire{Bids: [][]float64{{99, 2}}, Asks: [][]float64{{102, 3}}},
	}); err != nil {
		t.Fatal(err)
	}
	markets, err = adapter.FetchMarketData(context.Background())
	if err != nil || len(markets) != 1 {
		t.Fatalf("current generation did not republish market: %+v, err = %v", markets, err)
	}
	stream.markDisconnected(nextGeneration)
	adapter.now = func() time.Time { return now.Add(marketSnapshotMaxAge + time.Second) }
	markets, err = adapter.FetchMarketData(context.Background())
	if err != nil || len(markets) != 0 {
		t.Fatalf("expired disconnected markets = %+v, err = %v", markets, err)
	}
}

func TestAdapterReportsInactiveAssetsWithoutCategoryFiltering(t *testing.T) {
	stream := NewMarketStream(nil, discardLogger())
	stream.replaceMetadata(ExchangeMetadata{
		Accounts: ExchangeAccounts{PerpAssetMap: "map", GlobalTraderIndex: []string{"index"}, ActiveTraderBuffer: []string{"buffer"}},
		Markets: []MarketMetadata{
			{Symbol: "SOL", Asset: "SOL", Status: "active"},
			{Symbol: "AAPL", Asset: "AAPL", Status: "closed"},
			{Symbol: "OIL", Asset: "OIL", Status: "paused"},
		},
	})
	adapter := &Adapter{stream: stream, logger: discardLogger(), now: time.Now}
	assets := adapter.InactiveAssets()
	if len(assets) != 2 || assets[0] != "AAPL" || assets[1] != "OIL" {
		t.Fatalf("inactive assets = %v", assets)
	}
}
