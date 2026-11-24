package hyperliquid

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
)

func TestOpenInterestCapListValidationFreshnessAndRecovery(t *testing.T) {
	for _, invalid := range []string{`null`, `{}`, `[1]`, `[null]`, `[""]`, `[" SAGA"]`, `["SAGA",1]`, `["SAGA"] {}`} {
		t.Run(invalid, func(t *testing.T) {
			body, status := `["SAGA"]`, http.StatusOK
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var payload map[string]string
				if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" || json.NewDecoder(r.Body).Decode(&payload) != nil || payload["type"] != "perpsAtOpenInterestCap" {
					t.Error("incorrect cap request")
				}
				w.WriteHeader(status)
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()
			a := New(slog.Default())
			a.marketURL = server.URL
			now := time.Now()
			a.now = func() time.Time { return now }
			if a.OpenBlockDataReady() {
				t.Fatal("missing list is ready")
			}
			if err := a.pollOpenInterestCap(context.Background()); err != nil {
				t.Fatal(err)
			}
			want := map[string]string{"SAGA": venue.OpenBlockOpenInterestCap}
			if !a.OpenBlockDataReady() || !reflect.DeepEqual(a.OpenBlockedAssets(), want) {
				t.Fatal("valid list not published")
			}
			copy := a.OpenBlockedAssets()
			delete(copy, "SAGA")
			body = invalid
			now = now.Add(time.Minute)
			if err := a.pollOpenInterestCap(context.Background()); err == nil {
				t.Fatal("invalid response accepted")
			}
			if !a.OpenBlockDataReady() || !reflect.DeepEqual(a.OpenBlockedAssets(), want) {
				t.Fatal("invalid response erased valid list")
			}
			status = http.StatusTooManyRequests
			if err := a.pollOpenInterestCap(context.Background()); err == nil {
				t.Fatal("HTTP error accepted")
			}
			now = now.Add(2*time.Minute + time.Nanosecond)
			if a.OpenBlockDataReady() {
				t.Fatal("failed reads renewed list freshness")
			}
			if !reflect.DeepEqual(a.OpenBlockedAssets(), want) {
				t.Fatal("stale list disappeared from discovery")
			}
			body, status = `[]`, http.StatusOK
			if err := a.pollOpenInterestCap(context.Background()); err != nil {
				t.Fatal(err)
			}
			if !a.OpenBlockDataReady() || len(a.OpenBlockedAssets()) != 0 {
				t.Fatal("fresh empty list did not lift cap")
			}
		})
	}
}
