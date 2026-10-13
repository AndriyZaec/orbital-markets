package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/api/middleware"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
)

func TestCloseAndSubmitReadHTTPBodiesWithoutEarlyFlush(t *testing.T) {
	for _, http2 := range []bool{false, true} {
		for _, chunked := range []bool{false, true} {
			t.Run(fmt.Sprintf("http2_%t_chunked_%t", http2, chunked), func(t *testing.T) {
				s, _ := newResidualExposureServer(t)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				// A definite rejection proves the close reached the venue boundary
				// without starting any asynchronous fill/close-confirmation work.
				feed := &localPrepareFeed{fakeAccountFeed: &fakeAccountFeed{submitResult: &domain.SubmissionResult{Accepted: false, Error: "scripted close rejection"}}}
				s.live.accounts = newAccountFeedRegistry(ctx, map[string]accountFeedFactory{
					"pacifica": &fixedAccountFeedFactory{feed: feed},
				}, accountFeedRegistryConfig{})
				mux := http.NewServeMux()
				mux.HandleFunc("POST /api/v1/live/close/{id}", s.handleLiveClose)
				mux.HandleFunc("POST /api/v1/live/submit", s.handleLiveSubmit)
				server := httptest.NewUnstartedServer(middleware.Logging(s.logger)(mux))
				server.EnableHTTP2 = http2
				if http2 {
					server.StartTLS()
				} else {
					server.Start()
				}
				defer server.Close()
				client := server.Client()
				client.Timeout = 3 * time.Second
				post := func(path string, body any, result any) {
					t.Helper()
					data, err := json.Marshal(body)
					if err != nil {
						t.Fatal(err)
					}
					request, err := http.NewRequest(http.MethodPost, server.URL+path, bytes.NewReader(data))
					if err != nil {
						t.Fatal(err)
					}
					request.Header.Set("Content-Type", "application/json")
					// Close/submit should still return normal JSON, even with this Accept.
					request.Header.Set("Accept", "application/x-ndjson")
					if chunked {
						request.ContentLength = -1
					}
					response, err := client.Do(request)
					if err != nil {
						t.Fatal(err)
					}
					defer response.Body.Close()
					if http2 && response.ProtoMajor != 2 {
						t.Fatalf("expected HTTP/2, got %s", response.Proto)
					}
					if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "application/json" {
						t.Fatalf("%s status=%d content-type=%s", path, response.StatusCode, response.Header.Get("Content-Type"))
					}
					if err := json.NewDecoder(response.Body).Decode(result); err != nil {
						t.Fatal(err)
					}
				}
				var prepared struct {
					Requests []*domain.SigningRequest `json:"signing_requests"`
				}
				post("/api/v1/live/close/position-residual", map[string]string{
					"account_pacifica": "sol-wallet", "account_hyperliquid": "0xwallet",
					"agent_pacifica": "sol-agent", "agent_hyperliquid": "0xagent",
				}, &prepared)
				if len(prepared.Requests) != 1 || !prepared.Requests[0].ReduceOnly || prepared.Requests[0].Amount != 2.75 {
					t.Fatalf("unexpected close requests: %+v", prepared.Requests)
				}
				if feed.submissions.Load() != 0 {
					t.Fatal("close preparation submitted an order")
				}
				request := prepared.Requests[0]
				var submitted domain.SubmissionResult
				post("/api/v1/live/submit", domain.SignedAction{
					RequestID: request.ID, ClientOrderID: request.ClientOrderID, Venue: request.Venue,
					SignerAddress: request.Signer, Signature: "fixture-signature",
				}, &submitted)
				if submitted.Accepted || submitted.Error != "scripted close rejection" || feed.submissions.Load() != 1 {
					t.Fatalf("submit did not reach scripted close venue: %+v", submitted)
				}
			})
		}
	}
}
