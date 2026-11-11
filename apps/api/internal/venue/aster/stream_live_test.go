package aster

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix"
	"github.com/gorilla/websocket"
)

// Opt-in public, read-only measurement; both connections sample concurrently.
// ASTER_LIVE_MEASURE=1 go test ./internal/venue/aster -run TestLiveBookTraffic -v
func TestLiveBookTraffic(t *testing.T) {
	if os.Getenv("ASTER_LIVE_MEASURE") != "1" {
		t.Skip("set ASTER_LIVE_MEASURE=1 for a 60-second public feed measurement")
	}
	ctx := context.Background()
	client := &http.Client{Timeout: 20 * time.Second}
	fetch := func(endpoint string, body []byte, target any) {
		t.Helper()
		method := http.MethodGet
		if body != nil {
			method = http.MethodPost
		}
		req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: HTTP %d", endpoint, resp.StatusCode)
		}
		if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
			t.Fatal(err)
		}
	}
	var hl struct {
		Universe []struct {
			Name       string `json:"name"`
			IsDelisted bool   `json:"isDelisted"`
		} `json:"universe"`
	}
	fetch("https://api.hyperliquid.xyz/info", []byte(`{"type":"meta"}`), &hl)
	var pac struct {
		Data []struct {
			Symbol string `json:"symbol"`
		} `json:"data"`
	}
	fetch("https://api.pacifica.fi/api/v1/info", nil, &pac)
	metadata, err := phoenix.NewClient(phoenix.DefaultRESTURL, client).FetchExchangeMetadata(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var assets []string
	for _, market := range hl.Universe {
		if !market.IsDelisted {
			assets = append(assets, market.Name)
		}
	}
	for _, market := range pac.Data {
		assets = append(assets, market.Symbol)
	}
	for _, market := range metadata.Markets {
		if market.Status == "active" {
			assets = append(assets, market.Asset)
		}
	}
	base := newTestAdapter(defaultRESTURL)
	if err := base.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"all_market", "per_symbol"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			a := newTestAdapter(defaultRESTURL)
			a.streamConnected = true
			a.wsURL = defaultWSURL
			for symbol, state := range base.markets {
				a.markets[symbol] = state
			}
			a.SetCrossVenueAssets(assets)
			streams, _ := a.demandedBookStreams()
			t.Logf("Aster markets=%d, allowed books=%d, Phoenix markets=%d", len(a.markets), len(streams), len(metadata.Markets))
			endpoint, allBooks, updates, err := a.bookSubscriptionURL()
			if err != nil {
				t.Fatal(err)
			}
			if mode == "all_market" {
				endpoint = defaultWSURL
			}
			conn, _, err := websocket.DefaultDialer.Dial(endpoint, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			errs := make(chan error, 1)
			fail := func(err error) {
				select {
				case errs <- err:
				default:
				}
			}
			var messages, payloadBytes, books, bookBytes, marks, markBytes atomic.Uint64
			startReader := func(conn *websocket.Conn) <-chan streamSubscriptionAck {
				acks := make(chan streamSubscriptionAck, 1)
				go func() {
					<-ctx.Done()
					_ = conn.Close()
				}()
				go func() {
					for {
						_, raw, err := conn.ReadMessage()
						if err != nil {
							fail(err)
							return
						}
						if bytes.Contains(raw, []byte(`"id":`)) {
							var ack streamSubscriptionAck
							if err := json.Unmarshal(raw, &ack); err != nil {
								fail(err)
								return
							}
							select {
							case acks <- ack:
							case <-ctx.Done():
								return
							}
							continue
						}
						messages.Add(1)
						payloadBytes.Add(uint64(len(raw)))
						if bytes.Contains(raw, []byte("bookTicker")) {
							books.Add(1)
							bookBytes.Add(uint64(len(raw)))
						} else {
							marks.Add(1)
							markBytes.Add(uint64(len(raw)))
						}
						if err := a.applyStreamMessage(raw); err != nil {
							fail(err)
							return
						}
					}
				}()
				return acks
			}
			acks := startReader(conn)
			if mode == "per_symbol" {
				openShard := func(streams []string) (*websocket.Conn, <-chan streamSubscriptionAck, error) {
					endpoint, err := a.bookShardURL(streams)
					if err != nil {
						return nil, nil, err
					}
					conn, _, err := websocket.DefaultDialer.DialContext(ctx, endpoint, nil)
					if err != nil {
						return nil, nil, err
					}
					return conn, startReader(conn), nil
				}
				go func() {
					if err := a.maintainBookSubscriptions(ctx, conn, allBooks, updates, acks, openShard); err != nil {
						fail(err)
					}
				}()
			}
			time.Sleep(2 * time.Second) // Warm up subscriptions before counting.
			start := []uint64{messages.Load(), payloadBytes.Load(), books.Load(), bookBytes.Load(), marks.Load(), markBytes.Load()}
			began := time.Now()
			select {
			case err := <-errs:
				t.Fatal(err)
			case <-time.After(60 * time.Second):
			}
			elapsed := time.Since(began).Seconds()
			end := []uint64{messages.Load(), payloadBytes.Load(), books.Load(), bookBytes.Load(), marks.Load(), markBytes.Load()}
			rates := make([]float64, len(end))
			for i := range end {
				rates[i] = float64(end[i]-start[i]) / elapsed
			}
			t.Logf("%.3fs total: %.1f msg/s %.1f KB/s; book: %.1f msg/s %.1f KB/s; mark: %.1f msg/s %.1f KB/s (decoded WS payload, decimal KB)", elapsed, rates[0], rates[1]/1000, rates[2], rates[3]/1000, rates[4], rates[5]/1000)
		})
	}
}
