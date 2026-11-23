package phoenix

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/scanner"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
)

func openingCatalog(oi, cap any, isolated any) map[string]any {
	return map[string]any{
		"keys": map[string]any{"perpAssetMap": "map", "globalTraderIndex": []string{"index"}, "activeTraderBuffer": []string{"buffer"}},
		"markets": []any{map[string]any{
			"symbol": "SOL", "marketStatus": "active", "marketPubkey": "market", "splinePubkey": "spline", "tickSize": 100, "baseLotsDecimals": 2,
			"fundingIntervalSeconds": 3600, "leverageTiers": []any{map[string]any{"maxLeverage": 20}},
			"statsSnapshot": map[string]any{"openInterestBaseLots": oi}, "openInterestCapBaseLots": cap, "isolatedOnly": isolated,
		}},
	}
}

func TestPhoenixOpeningChecksExactRESTBaseLots(t *testing.T) {
	for _, tc := range []struct {
		name              string
		oi, cap, isolated any
		amount            float64
		want              string
		wantErr           bool
	}{
		{"below", "900", "1000", false, .99, "", false},
		{"exact cap", "900", "1000", false, 1, "", false},
		{"exceeds cap", "900", "1000", false, 1.01, venue.OpenBlockOpenInterestCap, false},
		{"at cap", "1000", "1000", false, .01, venue.OpenBlockOpenInterestCap, false},
		{"above cap", "1001", "1000", false, .01, venue.OpenBlockOpenInterestCap, false},
		{"isolated", "1", "1000", true, .01, venue.OpenBlockIsolatedOnly, false},
		{"beyond float integer precision", "9007199254740993", "9007199254740993", false, .01, venue.OpenBlockOpenInterestCap, false},
		{"sum overflow", "18446744073709551615", "18446744073709551615", false, .01, venue.OpenBlockOpenInterestCap, false},
		{"missing OI", nil, "1000", false, .01, "", true},
		{"missing cap", "1", nil, false, .01, "", true},
		{"missing isolated policy", "1", "1000", nil, .01, "", true},
		{"fractional lots", "1", "1000", false, .001, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reads := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/view/exchange" {
					t.Error(r.URL.Path)
				}
				reads++
				_ = json.NewEncoder(w).Encode(openingCatalog(tc.oi, tc.cap, tc.isolated))
			}))
			defer server.Close()
			a := &Adapter{stream: NewMarketStream(NewClient(server.URL, server.Client()), discardLogger())}
			reason, err := a.CheckOpenOrder(context.Background(), "SOL", tc.amount)
			if (err != nil) != tc.wantErr || reason != tc.want || reads != 1 {
				t.Fatalf("reason=%s err=%v reads=%d", reason, err, reads)
			}
		})
	}
}

type openingCounterpart struct{}

func (openingCounterpart) Name() string { return "hyperliquid" }
func (openingCounterpart) FetchMarketData(context.Context) ([]venue.MarketData, error) {
	return []venue.MarketData{{Venue: "hyperliquid", Asset: "SOL", MarketKey: "SOL", MarkPrice: 100, IndexPrice: 100, BidPrice: 99, AskPrice: 101, BidSize: 10000, AskSize: 10000, FundingRate: .002, OpenInterest: 10000, MaxLeverage: 20, Timestamp: time.Now()}}, nil
}

func TestPhoenixBlockedMarketsDisappearFromOpportunities(t *testing.T) {
	for _, reason := range []string{venue.OpenBlockIsolatedOnly, venue.OpenBlockOpenInterestCap} {
		t.Run(reason, func(t *testing.T) {
			now := time.Now()
			stream := NewMarketStream(nil, discardLogger())
			market := MarketMetadata{Symbol: "SOL", Asset: "SOL", Status: "active", AmountIncrement: .01, FundingInterval: time.Hour, MaxLeverage: 20, OpenInterestCapBaseLots: 1000, OpeningPolicyKnown: true}
			stream.replaceMetadata(ExchangeMetadata{Markets: []MarketMetadata{market}})
			generation := stream.markConnected()
			stream.haveExchangeSnapshot = true
			if err := stream.applyMarketStats(generation, marketStatsV2Envelope{Stats: []marketStatsV2Wire{{Symbol: "SOL", Timestamp: now.Unix(), MarkPrice: 100, OraclePrice: 100, OpenInterest: 9, CurrentFundingRate: testFundingRate(.01)}}}); err != nil {
				t.Fatal(err)
			}
			if err := stream.applyOrderbook(generation, orderbookEnvelope{Symbol: "SOL", Orderbook: orderbookWire{Bids: [][]float64{{99, 100}}, Asks: [][]float64{{101, 100}}}}); err != nil {
				t.Fatal(err)
			}
			a := &Adapter{stream: stream, logger: discardLogger(), now: time.Now}
			s := scanner.New(discardLogger(), a, openingCounterpart{})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go s.Run(ctx, time.Hour)
			deadline := time.Now().Add(time.Second)
			var id string
			for time.Now().Before(deadline) {
				if opps := s.Opportunities(); len(opps) > 0 {
					id = opps[0].ID
					break
				}
				time.Sleep(time.Millisecond)
			}
			if id == "" {
				t.Fatal("initial opportunity missing")
			}
			update := `{"kind":"isolatedOnlyUpdated","new":true}`
			if reason == venue.OpenBlockOpenInterestCap {
				update = `{"kind":"openInterestCapUpdated","new_base_lots":"900"}`
			}
			stream.mu.Lock()
			err := applyMarketParameterUpdate(stream.markets, "SOL", json.RawMessage(update))
			stream.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			if a.OpenBlockedAssets()["SOL"] != reason || len(s.Opportunities()) != 0 {
				t.Fatalf("blocked market still published: %v", a.OpenBlockedAssets())
			}
			if _, err := s.BuildPlan(ctx, id, 2, 100); err == nil {
				t.Fatal("blocked market got a plan")
			}
			if len(a.InactiveAssets()) != 0 {
				t.Fatal("temporary restriction became delisting")
			}
		})
	}
}

func TestOpeningPolicySurvivesWSNormalization(t *testing.T) {
	var config wsMarketConfig
	if err := json.Unmarshal([]byte(`{"symbol":"SOL","marketStatus":"active","marketPubkey":"market","splinePubkey":"spline","tickSize":100,"baseLotsDecimals":2,"fundingConfig":{"fundingIntervalSeconds":3600},"leverageTiers":[{"maxLeverage":20}],"openInterestCapBaseLots":"9007199254740993","isolatedOnly":true}`), &config); err != nil {
		t.Fatal(err)
	}
	market, err := normalizeWSMarket(config)
	if err != nil || !market.OpeningPolicyKnown || !market.IsolatedOnly || market.OpenInterestCapBaseLots != 9007199254740993 {
		t.Fatal(market, err)
	}
}
