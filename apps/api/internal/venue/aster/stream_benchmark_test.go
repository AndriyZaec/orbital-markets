package aster

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

func BenchmarkApplyStreamMessage(b *testing.B) {
	book := func(symbol string) []byte {
		return []byte(fmt.Sprintf(`{"stream":"!bookTicker","data":{"e":"bookTicker","u":9938451920,"E":1794300000123,"T":1794300000121,"s":%q,"b":"0.053600","B":"182930.000","a":"0.053610","A":"273081.000"}}`, symbol))
	}
	allowed, excluded := book("BTCUSDT"), book("LOCALUSDT")
	marks := make([]markPriceUpdate, 552)
	for i := range marks {
		marks[i] = markPriceUpdate{
			EventType: "markPriceUpdate", EventTime: 1794300000123,
			Symbol: fmt.Sprintf("ASSET%dUSDT", i), MarkPrice: "0.05360500",
			EstimatedSettlePrice: "0.05360900", IndexPrice: "0.05360400", FundingRate: "0.00010000",
		}
	}
	markFrame, err := json.Marshal(struct {
		Stream string            `json:"stream"`
		Data   []markPriceUpdate `json:"data"`
	}{"!markPrice@arr@1s", marks})
	if err != nil {
		b.Fatal(err)
	}
	for _, name := range []string{"book_allowed", "book_excluded", "book_mixed_58pct_excluded", "mark_array_552"} {
		b.Run(name, func(b *testing.B) {
			a := newTestAdapter("http://unused")
			a.streamConnected = true
			a.markets["BTCUSDT"] = marketState{marketMetadata: marketMetadata{asset: "BTC"}}
			a.markets["LOCALUSDT"] = marketState{marketMetadata: marketMetadata{asset: "LOCAL"}}
			for i, mark := range marks {
				a.markets[mark.Symbol] = marketState{marketMetadata: marketMetadata{asset: fmt.Sprintf("ASSET%d", i)}}
			}
			// Also runs unchanged against the pre-filter adapter for a baseline.
			if sink, ok := any(a).(interface{ SetCrossVenueAssets([]string) }); ok {
				sink.SetCrossVenueAssets([]string{"BTC"})
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				frame := allowed
				switch name {
				case "book_excluded":
					frame = excluded
				case "book_mixed_58pct_excluded":
					if i%100 < 58 {
						frame = excluded
					}
				case "mark_array_552":
					frame = markFrame
				}
				if err := a.applyStreamMessage(frame); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// One operation is the same source workload: 100 all-market updates, of which
// 42 reach us after server-side filtering. Includes TLS, WS read, and apply.
func BenchmarkReadAndApplyStream(b *testing.B) {
	for _, mode := range []string{"all_market_100", "per_symbol_42"} {
		b.Run(mode, func(b *testing.B) {
			a := newTestAdapter("http://unused")
			a.streamConnected = true
			a.markets["BTCUSDT"] = marketState{marketMetadata: marketMetadata{asset: "BTC"}}
			a.markets["LOCALUSDT"] = marketState{marketMetadata: marketMetadata{asset: "LOCAL"}}
			a.SetCrossVenueAssets([]string{"BTC"})
			allowed, excluded := filterBookFrame("BTCUSDT"), filterBookFrame("LOCALUSDT")
			frames := 100
			if mode == "per_symbol_42" {
				frames = 42
				allowed = []byte(strings.Replace(string(allowed), `"!bookTicker"`, `"btcusdt@bookTicker"`, 1))
			}
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer conn.Close()
				for i := 0; i < b.N; i++ {
					for j := 0; j < frames; j++ {
						frame := allowed
						if mode == "all_market_100" && j < 58 {
							frame = excluded
						}
						if err := conn.WriteMessage(websocket.TextMessage, frame); err != nil {
							return
						}
					}
				}
			}))
			defer server.Close()
			dialer := websocket.Dialer{TLSClientConfig: server.Client().Transport.(*http.Transport).TLSClientConfig}
			conn, _, err := dialer.Dial("wss"+strings.TrimPrefix(server.URL, "https"), nil)
			if err != nil {
				b.Fatal(err)
			}
			defer conn.Close()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				for j := 0; j < frames; j++ {
					_, raw, err := conn.ReadMessage()
					if err != nil {
						b.Fatal(err)
					}
					if err := a.applyStreamMessage(raw); err != nil {
						b.Fatal(err)
					}
				}
			}
			b.StopTimer()
			b.ReportMetric(float64(frames), "messages/op")
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*frames), "ns/message")
		})
	}
}
