package funding

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix"
)

type testMarkets struct{}

func (testMarkets) Snapshot() phoenix.MarketStreamSnapshot {
	return phoenix.MarketStreamSnapshot{Metadata: phoenix.ExchangeMetadata{Markets: []phoenix.MarketMetadata{{Symbol: "SOL-PERP", Asset: "SOL"}, {Symbol: "BTC", Asset: "BTC"}}}}
}

const owner = "11111111111111111111111111111111"

func event(at time.Time, symbol, amount string) map[string]any {
	return map[string]any{"timestamp": at.UTC().Format(time.RFC3339Nano), "symbol": symbol, "fundingPayment": amount, "fundingRatePercentage": "0.001", "positionSize": "1", "positionSide": "Long"}
}

func TestSettlementReaderScopesSymbolsAndDeduplicatesCursorOverlap(t *testing.T) {
	since := time.Now().UTC().Add(-48 * time.Hour)
	until := since.Add(48 * time.Hour)
	first, second := event(since.Add(time.Hour), "SOL-PERP", "-0.123"), event(since.Add(25*time.Hour), "SOL-PERP", "0.1")
	var reads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads.Add(1)
		query := r.URL.Query()
		if r.URL.Path != "/v1/trader/"+owner+"/funding-history" || query.Get("symbol") != "SOL-PERP" || query.Get("traderPdaIndex") != "0" || query.Has("resolution") {
			t.Error("wrong settlement scope", r.URL)
		}
		if query.Get("cursor") == "" {
			if query.Get("startTime") == "" || query.Get("endTime") == "" {
				t.Error("unbounded funding window")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"events": []any{first, event(since.Add(time.Hour), "BTC", "900")}, "hasMore": true, "nextCursor": "older+opaque="})
		} else {
			if query.Has("startTime") || query.Get("cursor") != "older+opaque=" || query.Get("endTime") == "" {
				t.Error("cursor/startTime contract violated", r.URL)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"events": []any{first, second}, "hasMore": false})
		}
	}))
	defer server.Close()
	reader := NewReader(server.URL, server.Client(), testMarkets{})
	payments, err := reader.FundingPayments(context.Background(), owner, "SOL", since, until)
	if err != nil || len(payments) != 2 || reads.Load() != 2 {
		t.Fatal(payments, err)
	}
	if math.Abs(payments[0].AmountUSD+payments[1].AmountUSD+.023) > 1e-12 {
		t.Fatal("signed funding was changed", payments)
	}
	for _, payment := range payments {
		if payment.MarketKey != "SOL-PERP" || payment.Asset != "SOL" || payment.Account != owner || !strings.HasPrefix(payment.ExternalID, "phoenix-settlement:") {
			t.Fatal(payment)
		}
	}
	again, err := reader.FundingPayments(context.Background(), owner, "SOL", since, until)
	if err != nil || again[0].ExternalID != payments[0].ExternalID {
		t.Fatal("settlement identity changed across overlap/restart", again, err)
	}
}

func TestSettlementReaderRejectsHourlyAccrualConflictsAndIncompleteCoverage(t *testing.T) {
	for _, kind := range []string{"hourly", "hourly-string", "duplicate", "cursor", "cross", "owner"} {
		t.Run(kind, func(t *testing.T) {
			since := time.Now().UTC().Add(-time.Hour)
			until := time.Now().UTC()
			row := event(since.Add(30*time.Minute), "SOL-PERP", "1")
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				body := map[string]any{"events": []any{row}, "hasMore": false}
				switch kind {
				case "hourly":
					row["timestamp"] = time.Now().Unix()
				case "hourly-string":
					row["timestamp"] = "1770825600"
				case "duplicate":
					other := event(since.Add(30*time.Minute), "SOL-PERP", "2")
					body["events"] = []any{row, other}
				case "cursor":
					body["hasMore"] = true
					body["nextCursor"] = "same"
				case "cross":
					row["subaccountIndex"] = 1
				case "owner":
					body["authority"] = "wrong-owner"
				}
				_ = json.NewEncoder(w).Encode(body)
			}))
			defer server.Close()
			if payments, err := NewReader(server.URL, server.Client(), testMarkets{}).FundingPayments(context.Background(), owner, "SOL", since, until); err == nil || payments != nil {
				t.Fatal("invalid/accrual batch advanced settlement coverage", payments, err)
			}
		})
	}
}

func TestSettlement429BackoffAndDailyFinalizationDelay(t *testing.T) {
	var reads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reads.Add(1)
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(429)
	}))
	defer server.Close()
	reader := NewReader(server.URL, server.Client(), testMarkets{})
	for range 2 {
		if _, err := reader.FundingPayments(context.Background(), owner, "SOL", time.Now().Add(-time.Hour), time.Now()); err == nil {
			t.Fatal("429 became successful empty coverage")
		}
	}
	if reads.Load() != 1 || reader.FundingFinalizationDelay() <= 24*time.Hour {
		t.Fatal("backoff/finalization contract", reads.Load())
	}
}
