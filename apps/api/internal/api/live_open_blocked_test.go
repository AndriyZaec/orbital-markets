package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"testing"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/executor"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/scanner"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
)

func TestPrepareRejectsOpenCapOrUnavailableStatusBeforeSigning(t *testing.T) {
	for _, scenario := range []string{"cap", "missing", "stale"} {
		for _, streamed := range []bool{false, true} {
			t.Run(scenario+map[bool]string{false: "/json", true: "/stream"}[streamed], func(t *testing.T) {
				f := newLocalPrepareFixture(t)
				module, _ := f.s.live.modules.Module("hyperliquid")
				checkpoint := &admissionCheckpointModule{LiveModule: module, name: "hyperliquid"}
				primary, _ := f.s.live.modules.Module("pacifica")
				var err error
				f.s.live.modules, err = venue.NewLiveModuleRegistry(primary, checkpoint)
				if err != nil {
					t.Fatal(err)
				}
				a := f.markets[1]
				a.mu.Lock()
				a.openBlockUnready = scenario != "cap"
				if scenario == "cap" {
					a.openBlocked = map[string]string{"BTC": venue.OpenBlockOpenInterestCap}
				}
				a.mu.Unlock()
				status, result, body, _ := f.prepare(t, context.Background(), streamed)
				var response map[string]string
				if err := json.Unmarshal([]byte(body), &response); err != nil {
					t.Fatal(err)
				}
				want := "OPEN_MARKET_STATUS_UNAVAILABLE"
				if scenario == "cap" {
					want = "OPEN_MARKET_BLOCKED"
				}
				if status != http.StatusUnprocessableEntity || response["code"] != want {
					t.Fatalf("prepare=%d %s", status, body)
				}
				if scenario == "cap" && response["error"] != "Hyperliquid isn't accepting new positions in BTC right now. Choose another opportunity." {
					t.Fatal(response)
				}
				if len(result.SigningRequests) != 0 || result.SessionID != "" || checkpoint.reached || f.depth.calls != 0 {
					t.Fatal("blocked prepare reached signing or execution")
				}
				// The fixture also asserts that neither venue submitted an order.
			})
		}
	}
}

func TestHyperliquidCapDoesNotBlockCloseOrKill(t *testing.T) {
	for _, emergency := range []bool{false, true} {
		s := newClosePriceServer(t, "hyperliquid", closePriceMarket(0, true))
		s.scanner = scanner.New(slog.Default(), &prepareMarketAdapter{
			data:             venue.MarketData{Venue: "hyperliquid"},
			openBlocked:      map[string]string{"BTC": venue.OpenBlockOpenInterestCap},
			openBlockUnready: true,
		})
		if err := s.scanner.CheckOpenAvailability("BTC", "hyperliquid"); err == nil {
			t.Fatal("test market allows opening")
		}
		request, err := s.buildCloseSigningRequestForBindings(context.Background(),
			executor.LiveFill{Venue: "hyperliquid", Symbol: "BTC", Side: "long", FilledAmount: 2, AvgFillPrice: 100},
			"BTC", "cap-close", liveVenueBindings{
				Accounts: map[string]string{"hyperliquid": "0x1111111111111111111111111111111111111111"},
				Agents:   map[string]string{"hyperliquid": "0x2222222222222222222222222222222222222222"},
			}, emergency)
		if err != nil || request == nil || !request.ReduceOnly || request.Amount != 2 {
			t.Fatalf("close/kill blocked by cap: %+v %v", request, err)
		}
	}
}
