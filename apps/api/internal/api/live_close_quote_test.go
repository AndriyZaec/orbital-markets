package api

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/executor"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
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
