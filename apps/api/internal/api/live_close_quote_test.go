package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/executor"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
	hllive "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/hyperliquid/live"
)

type closeQuoteTestSource struct {
	snapshot venue.MarketData
	err      error
}

func (s closeQuoteTestSource) MarketSnapshot(context.Context, string, string) (venue.MarketData, error) {
	return s.snapshot, s.err
}

type closeQuoteTestAssetMap struct{}

func (closeQuoteTestAssetMap) AssetIndex(string) (int, bool)   { return 213, true }
func (closeQuoteTestAssetMap) SizeDecimals(string) (int, bool) { return 0, true }

func TestHyperliquidCloseUsesFreshExecutableSideBBO(t *testing.T) {
	server := &Server{
		closeMarkets: closeQuoteTestSource{snapshot: venue.MarketData{
			Venue: "hyperliquid", Asset: "2Z", BidPrice: 0.0536, BidSize: 500,
			AskPrice: 0.0537, AskSize: 500, Timestamp: time.Now(),
		}},
		live: &LiveDeps{hlAssetMap: closeQuoteTestAssetMap{}},
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
	server := &Server{
		closeMarkets: closeQuoteTestSource{snapshot: venue.MarketData{
			Venue: "hyperliquid", Asset: "2Z", BidPrice: 0.0536, BidSize: 500,
			AskPrice: 0.0537, AskSize: 500, Timestamp: time.Now().Add(-time.Minute),
		}},
		live: &LiveDeps{hlAssetMap: closeQuoteTestAssetMap{}},
	}

	_, err := server.buildCloseSigningRequest(
		context.Background(),
		executor.LiveFill{Venue: "hyperliquid", Symbol: "2Z", Side: string(domain.SideLong), FilledAmount: 265, AvgFillPrice: 0.0566},
		"close-order", "pacifica-owner", "hl-owner", "pacifica-agent", "hl-agent",
	)
	if err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("error = %v, want stale BBO rejection", err)
	}
}

func TestHyperliquidShortCloseUsesCurrentAsk(t *testing.T) {
	server := &Server{
		closeMarkets: closeQuoteTestSource{snapshot: venue.MarketData{
			Venue: "hyperliquid", Asset: "2Z", BidPrice: 0.0536, BidSize: 500,
			AskPrice: 0.0537, AskSize: 500, Timestamp: time.Now(),
		}},
		live: &LiveDeps{hlAssetMap: closeQuoteTestAssetMap{}},
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
