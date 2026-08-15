package pacifica

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestFundingPaymentsPreserveAccountPayoutSign(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Query().Get("account") != "wallet" {
			t.Fatalf("request = %s %s", r.Method, r.URL.String())
		}
		if r.Body != nil {
			body, _ := io.ReadAll(r.Body)
			if len(body) != 0 {
				t.Fatalf("GET body = %q", body)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":[
			{"history_id":1,"symbol":"SOL","payout":"2.617479","created_at":1759222804122},
			{"history_id":2,"symbol":"BTC","payout":"9","created_at":1759222804122}
		],"has_more":false}`))
	}))
	defer server.Close()

	adapter := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	adapter.client = server.Client()
	adapter.fundingURL = server.URL
	payments, err := adapter.FundingPayments(
		context.Background(), "wallet", "SOL",
		time.UnixMilli(1759222804000), time.UnixMilli(1759222805000),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(payments) != 1 || payments[0].ExternalID != "1" || payments[0].AmountUSD != 2.617479 {
		t.Fatalf("payments = %+v", payments)
	}
}
