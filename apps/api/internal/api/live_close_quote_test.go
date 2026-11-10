package api

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/executor"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
	asterlive "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/aster/live"
	hllive "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/hyperliquid/live"
)

func TestCloseClientOrderIDsFitAsterConstraints(t *testing.T) {
	positionID := "plan-fbdae6a2-6368-481f-bd9a-6180e0feff0e"
	now := time.Unix(1790087764, 499000000)
	allowed := regexp.MustCompile(`^[.A-Z:/a-z0-9_-]{1,36}$`)

	for _, action := range []string{"close", "kill"} {
		clientOrderID := closeClientOrderID(action, positionID, 1, now)
		if !allowed.MatchString(clientOrderID) {
			t.Fatalf("%s client order ID %q does not satisfy Aster constraints", action, clientOrderID)
		}
	}
}

type closeQuoteTestSource struct {
	snapshot       venue.MarketData
	err            error
	requestedAsset *string
}

func (s closeQuoteTestSource) MarketSnapshot(_ context.Context, _, asset string) (venue.MarketData, error) {
	if s.requestedAsset != nil {
		*s.requestedAsset = asset
	}
	return s.snapshot, s.err
}

type closeQuoteTestAssetMap struct{}

func (closeQuoteTestAssetMap) AssetIndex(string) (int, bool)   { return 213, true }
func (closeQuoteTestAssetMap) SizeDecimals(string) (int, bool) { return 0, true }

func TestHyperliquidCloseUsesFreshExecutableSideBBO(t *testing.T) {
	modules, err := venue.NewLiveModuleRegistry(hllive.NewLiveModule(closeQuoteTestAssetMap{}))
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{
		closeMarkets: closeQuoteTestSource{snapshot: venue.MarketData{
			Venue: "hyperliquid", Asset: "2Z", BidPrice: 0.0536, BidSize: 500,
			AskPrice: 0.0537, AskSize: 500, Timestamp: time.Now(),
		}},
		live: &LiveDeps{modules: modules},
	}

	request, err := server.buildCloseSigningRequest(
		context.Background(),
		executor.LiveFill{Venue: "hyperliquid", Symbol: "2Z", Side: "long", FilledAmount: 265, AvgFillPrice: 0.0566},
		"close-order", "pacifica-owner", "hl-owner", "pacifica-agent", "hl-agent",
	)
	if err != nil {
		t.Fatal(err)
	}
	if request.Price != 0.0536 {
		t.Fatalf("close reference price = %v, want current bid 0.0536 instead of entry 0.0566", request.Price)
	}
	var unsigned hllive.HyperliquidUnsignedAction
	if err := json.Unmarshal(request.UnsignedPayload, &unsigned); err != nil {
		t.Fatal(err)
	}
	if got := unsigned.Action.Orders[0].LimitPx; got != "0.053332" {
		t.Fatalf("IOC limit = %q, want current bid with sell slippage", got)
	}
}

func TestHyperliquidCloseRejectsStaleBBOInsteadOfUsingEntryPrice(t *testing.T) {
	modules, err := venue.NewLiveModuleRegistry(hllive.NewLiveModule(closeQuoteTestAssetMap{}))
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{
		closeMarkets: closeQuoteTestSource{snapshot: venue.MarketData{
			Venue: "hyperliquid", Asset: "2Z", BidPrice: 0.0536, BidSize: 500,
			AskPrice: 0.0537, AskSize: 500, Timestamp: time.Now().Add(-time.Minute),
		}},
		live: &LiveDeps{modules: modules},
	}

	_, err = server.buildCloseSigningRequest(
		context.Background(),
		executor.LiveFill{Venue: "hyperliquid", Symbol: "2Z", Side: string(domain.SideLong), FilledAmount: 265, AvgFillPrice: 0.0566},
		"close-order", "pacifica-owner", "hl-owner", "pacifica-agent", "hl-agent",
	)
	if err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("error = %v, want stale BBO rejection", err)
	}
}

func TestMarketBBOCloseUsesBookHealthInsteadOfAggregateTimestamp(t *testing.T) {
	now := time.Now()
	for _, name := range []string{"hyperliquid", "aster"} {
		for _, emergency := range []bool{false, true} {
			for _, tc := range []struct {
				name      string
				aggregate time.Time
				book      *venue.ComponentHealth
				stale     bool
			}{
				{"fresh_REST_stale_BBO", now, &venue.ComponentHealth{Status: venue.HealthAvailable, SourceTime: now.Add(-11 * time.Second), ReceivedAt: now}, true},
				{"seven_second_book_still_valid_for_close", now, &venue.ComponentHealth{Status: venue.HealthAvailable, SourceTime: now.Add(-7 * time.Second)}, false},
				{"stale_aggregate_fresh_BBO", now.Add(-time.Minute), &venue.ComponentHealth{Status: venue.HealthAvailable, SourceTime: now}, false},
				{"fresh_received_time_without_source", now, &venue.ComponentHealth{Status: venue.HealthAvailable, ReceivedAt: now}, false},
				{"stale_received_time_without_source", now, &venue.ComponentHealth{Status: venue.HealthAvailable, ReceivedAt: now.Add(-11 * time.Second)}, true},
				{"unavailable_book", now, &venue.ComponentHealth{Status: venue.HealthUnavailable, SourceTime: now}, true},
				{"missing_book_time", now, &venue.ComponentHealth{Status: venue.HealthAvailable}, true},
				{"future_book_time", now, &venue.ComponentHealth{Status: venue.HealthAvailable, SourceTime: now.Add(time.Minute)}, true},
				{"legacy_fresh_timestamp", now, nil, false},
				{"legacy_stale_timestamp", now.Add(-11 * time.Second), nil, true},
				{"legacy_missing_timestamp", time.Time{}, nil, true},
			} {
				t.Run(fmt.Sprintf("%s/emergency=%t/%s", name, emergency, tc.name), func(t *testing.T) {
					var module venue.LiveModule = hllive.NewLiveModule(closeQuoteTestAssetMap{})
					if name == "aster" {
						module = asterlive.NewLiveModule(hedgeMatrixRules{}, nil)
					}
					modules, err := venue.NewLiveModuleRegistry(module)
					if err != nil {
						t.Fatal(err)
					}
					market := venue.MarketData{Venue: name, Asset: "BTC", BidPrice: 100, AskPrice: 101, BidSize: 500, AskSize: 500, Timestamp: tc.aggregate}
					if tc.book != nil {
						market.Health = &venue.MarketHealth{Book: *tc.book}
					}
					server := &Server{live: &LiveDeps{modules: modules}, closeMarkets: closeQuoteTestSource{snapshot: market}}
					request, err := server.buildCloseSigningRequestForBindings(context.Background(),
						executor.LiveFill{Venue: name, Symbol: "BTC", Side: "long", FilledAmount: 1, AvgFillPrice: 100},
						"BTC", "close-book-health", liveVenueBindings{
							Accounts: map[string]string{name: "0x1111111111111111111111111111111111111111"},
							Agents:   map[string]string{name: "0x2222222222222222222222222222222222222222"},
						}, emergency)
					if tc.stale {
						if request != nil || err == nil || !strings.Contains(err.Error(), "BBO for BTC is stale") {
							t.Fatalf("stale book reached close signing: request=%+v err=%v", request, err)
						}
					} else if err != nil || request == nil || request.Price <= 0 {
						t.Fatalf("fresh book rejected: request=%+v err=%v", request, err)
					}
				})
			}
		}
	}
}

func TestHyperliquidShortCloseUsesCurrentAsk(t *testing.T) {
	modules, err := venue.NewLiveModuleRegistry(hllive.NewLiveModule(closeQuoteTestAssetMap{}))
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{
		closeMarkets: closeQuoteTestSource{snapshot: venue.MarketData{
			Venue: "hyperliquid", Asset: "2Z", BidPrice: 0.0536, BidSize: 500,
			AskPrice: 0.0537, AskSize: 500, Timestamp: time.Now(),
		}},
		live: &LiveDeps{modules: modules},
	}

	request, err := server.buildCloseSigningRequest(
		context.Background(),
		executor.LiveFill{Venue: "hyperliquid", Symbol: "2Z", Side: string(domain.SideShort), FilledAmount: 265, AvgFillPrice: 0.0566},
		"close-order", "pacifica-owner", "hl-owner", "pacifica-agent", "hl-agent",
	)
	if err != nil {
		t.Fatal(err)
	}
	if request.Price != 0.0537 {
		t.Fatalf("close reference price = %v, want current ask 0.0537", request.Price)
	}
}
