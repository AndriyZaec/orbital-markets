package hyperliquid

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestFundingPaymentsPreserveAccountUSDCSign(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request["type"] != "userFunding" || request["user"] != "0xwallet" {
			t.Fatalf("request = %+v", request)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"delta":{"coin":"SOL","usdc":"-0.125"},"time":1759222804122,"hash":"0xabc"},
			{"delta":{"coin":"BTC","usdc":"5"},"time":1759222804122,"hash":"0xdef"}
		]`))
	}))
	defer server.Close()

	adapter := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	adapter.client = server.Client()
	adapter.fundingURL = server.URL
	payments, err := adapter.FundingPayments(
		context.Background(), "0xwallet", "SOL",
		time.UnixMilli(1759222804000), time.UnixMilli(1759222805000),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(payments) != 1 || payments[0].AmountUSD != -0.125 {
		t.Fatalf("payments = %+v", payments)
	}
}
