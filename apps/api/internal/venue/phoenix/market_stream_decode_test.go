package phoenix

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Frozen pre-optimization decoder: the equivalence oracle deliberately uses
// the full [][]float64 wire type and the original two envelope decodes.
func applyMessageBefore(s *MarketStream, generation uint64, raw []byte) error {
	var envelope wsEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("decode Phoenix websocket envelope: %w", err)
	}
	switch {
	case envelope.Channel == "exchange":
		var message exchangeEnvelope
		if err := json.Unmarshal(raw, &message); err != nil {
			return fmt.Errorf("decode Phoenix exchange message: %w", err)
		}
		return s.applyExchangeMessage(generation, message)
	case envelope.Channel == "marketStatsV2":
		var message marketStatsV2Envelope
		if err := json.Unmarshal(raw, &message); err != nil {
			return fmt.Errorf("decode Phoenix market stats: %w", err)
		}
		return s.applyMarketStats(generation, message)
	case envelope.Channel == "orderbook":
		var message orderbookEnvelope
		if err := json.Unmarshal(raw, &message); err != nil {
			return fmt.Errorf("decode Phoenix orderbook: %w", err)
		}
		return s.applyOrderbook(generation, message)
	case envelope.Channel == "error":
		return fmt.Errorf("Phoenix websocket server error %v: %s", envelope.Code, envelope.Error)
	case envelope.Type == "subscriptionError":
		return fmt.Errorf("Phoenix websocket subscription error %v: %s", envelope.Code, envelope.Message)
	case envelope.Type == "subscriptionConfirmed":
		return nil
	case envelope.Channel == "subscriptionStatus":
		if envelope.Status != "subscribed" && envelope.Status != "unsubscribed" {
			return fmt.Errorf("Phoenix websocket subscription status %q", envelope.Status)
		}
	}
	return nil
}

func assertMessageDecodeEquivalent(t *testing.T, raw []byte) {
	t.Helper()
	before, after := decodeBenchmarkStream(), decodeBenchmarkStream()
	seed := LiveBook{Symbol: "ASSET0", BidPrice: 99, BidSizeUSD: 198, AskPrice: 101, AskSizeUSD: 303, Generation: 1, ReceivedAt: time.Unix(10, 0).UTC()}
	before.books[seed.Symbol], after.books[seed.Symbol] = seed, seed
	oldErr := applyMessageBefore(before, 1, raw)
	newErr := after.applyMessage(1, raw)
	if fmt.Sprint(oldErr) != fmt.Sprint(newErr) {
		t.Fatalf("different errors for %s\nbefore: %v\nafter: %v", raw, oldErr, newErr)
	}
	if !reflect.DeepEqual(before.Snapshot(), after.Snapshot()) {
		t.Fatalf("different state for %s\nbefore: %+v\nafter: %+v", raw, before.Snapshot(), after.Snapshot())
	}
}

func TestMarketStreamDecodeEquivalent(t *testing.T) {
	for _, depth := range []int{1, 20, 100, 500} {
		t.Run(fmt.Sprintf("depth_%d", depth), func(t *testing.T) {
			assertMessageDecodeEquivalent(t, realisticBookFrame(depth))
		})
	}
	t.Run("stats", func(t *testing.T) { assertMessageDecodeEquivalent(t, realisticStatsFrame()) })
	for _, sides := range []string{
		`"bids":[],"asks":[[102,3]]`,
		`"bids":[[100,2]],"asks":[]`,
		`"bids":null,"asks":null`,
		`"bids":[[]],"asks":[[102,3]]`,
		`"bids":[null],"asks":[[102,3]]`,
		`"bids":[[100]],"asks":[[102,3]]`,
		`"bids":[[100,2,3]],"asks":[[102,3]]`,
		`"bids":[[100,-2]],"asks":[[102,3]]`,
		`"bids":[[100,0]],"asks":[[102,3]]`,
		`"bids":[[100,null]],"asks":[[102,3]]`,
		`"bids":[[100,2]],"asks":[[99,3]]`,
		`"bids":[[1e308,2]],"asks":[[1e308,3]]`,
		`"bids":[[100,2],[1e999,2]],"asks":[[102,3]]`,
		`"bids":[[100,2],["bad",2]],"asks":[[102,3]]`,
		`"bids":[[100,2],[false,2]],"asks":[[102,3]]`,
		`"bids":[[100,2],{}],"asks":[]`,
		`"bids":{},"asks":[]`,
		`"bids":1,"asks":[]`,
		`"bids":"wrong","asks":[]`,
		`"bids":[[100,2]],"asks":[[102,3],[[1],2]]`,
		`"bids":[[100,2]],"asks":[[102,3],null,[],[null,1e-999,-2,1e308]]`,
		`"bids":[[100,2],[` + strings.Repeat("9", 310) + `,2]],"asks":[[102,3]]`,
		`"bids":[[100,2],[` + strings.Repeat("9", 300) + `,2]],"asks":[[102,3]]`,
		`"bids":[[100,2],[0.` + strings.Repeat("0", 310) + `1,2]],"asks":[[102,3]]`,
	} {
		t.Run(sides, func(t *testing.T) {
			assertMessageDecodeEquivalent(t, []byte(`{"channel":"orderbook","symbol":"ASSET0","orderbook":{`+sides+`}}`))
		})
	}
	for _, frame := range []string{
		`{"channel":"orderbook","symbol":"ASSET0","bypassExecutionBand":true,"orderbook":{"bids":[],"asks":[]}}`,
		`{"channel":"orderbook","symbol":"ASSET0","orderbook":null}`,
		`{"channel":"orderbook","symbol":"ASSET0","orderbook":{"bids":[[100,2]],"asks":[[102,3]]},"orderbook":{"bids":[]}}`,
		`{"channel":"orderbook","symbol":"ASSET0","orderbook":{"bids":[],"bids":[[100,2]],"asks":[[102,3]]}}`,
		`{"symbol":"ASSET0", "orderbook":{"bids":[[100,2]],"asks":[[102,3]]}, "channel":"orderbook"}`,
		`{"channel":"orderbook","channel":"marketStatsV2","stats":[]}`,
		`{"channel":"orderbook","channel":null,"symbol":"ASSET0","orderbook":{"bids":[],"asks":[]}}`,
		`{"channel":"orderbook","symbol":"ASSET0","error":1,"orderbook":{"bids":[],"asks":[]}}`,
		`{"channel":"marketStatsV2","stats":[],"status":false}`,
		`{"channel":"marketStatsV2","stats":[{"symbol":"ASSET0","timestamp":1,"markPrice":"bad"}]}`,
		`{"channel":"exchange","messageType":"delta","version":1,"sequenceNumber":"2","ops":[]}`,
		`{"channel":"exchange","messageType":"snapshot","version":1,"sequenceNumber":"41","exchange":{"perpAssetMap":"map","globalTraderIndex":["index"],"activeTraderBuffer":["buffer"]},"markets":[{"symbol":"ASSET0","marketStatus":"active","marketPubkey":"market","splinePubkey":"spline","tickSize":100,"baseLotsDecimals":2,"fundingConfig":{"fundingIntervalSeconds":3600},"leverageTiers":[{"maxLeverage":20}]}]}`,
		`{"type":"subscriptionError","subscription":{"channel":"orderbook"},"code":"bad","message":"no"}`,
		`{"type":"subscriptionConfirmed","subscription":{"channel":"orderbook"}}`,
		`{"channel":"error","code":400,"error":"bad"}`,
		`{"channel":"subscriptionStatus","status":"failed"}`,
		`{"channel":"unknown","nested":{"channel":"orderbook"}}`,
		`{"channel":"orderbook","orderbook":{"bids":[[1,2]],"asks":[[3,4]]}}trailing`,
		`{"channel":"orderbook","orderbook":{"bids":[[1,2]],"asks":[[3,4]]}`,
	} {
		t.Run(frame, func(t *testing.T) { assertMessageDecodeEquivalent(t, []byte(frame)) })
	}
}

func FuzzMarketStreamDecodeEquivalent(f *testing.F) {
	f.Add(realisticBookFrame(1))
	f.Add([]byte(`{"channel":"marketStatsV2","stats":[{"symbol":"ASSET0","timestamp":1,"markPrice":100,"oraclePrice":101}]}`))
	f.Add([]byte(`{"channel":"orderbook","symbol":"ASSET0","orderbook":{"bids":[],"asks":[]}}`))
	f.Fuzz(func(t *testing.T, raw []byte) { assertMessageDecodeEquivalent(t, raw) })
}

func BenchmarkBookDecodeCandidates(b *testing.B) {
	for _, depth := range []int{20, 100, 500} {
		raw := realisticBookFrame(depth)
		b.Run(fmt.Sprintf("depth_%d", depth), func(b *testing.B) {
			for _, candidate := range []string{"full", "raw_sides", "top_sides"} {
				b.Run(candidate, func(b *testing.B) {
					b.ReportAllocs()
					for i := 0; i < b.N; i++ {
						switch candidate {
						case "full":
							var message orderbookEnvelope
							if err := json.Unmarshal(raw, &message); err != nil {
								b.Fatal(err)
							}
						case "raw_sides":
							var message struct {
								wsEnvelope
								Symbol    string `json:"symbol"`
								Orderbook struct {
									Bids json.RawMessage `json:"bids"`
									Asks json.RawMessage `json:"asks"`
								} `json:"orderbook"`
								BypassExecutionBand bool `json:"bypassExecutionBand"`
							}
							if err := json.Unmarshal(raw, &message); err != nil {
								b.Fatal(err)
							}
							for _, side := range []json.RawMessage{message.Orderbook.Bids, message.Orderbook.Asks} {
								var top topBookSide
								if err := json.Unmarshal(side, &top); err != nil {
									b.Fatal(err)
								}
							}
						case "top_sides":
							var message topOrderbookEnvelope
							if err := json.Unmarshal(raw, &message); err != nil {
								b.Fatal(err)
							}
						}
					}
				})
			}
		})
	}
}

func decodeBenchmarkStream() *MarketStream {
	s := NewMarketStream(nil, discardLogger())
	s.now = func() time.Time { return time.Unix(1793020210, 0).UTC() }
	s.markConnected()
	s.haveExchangeSnapshot = true
	for i := 0; i < 100; i++ {
		symbol := fmt.Sprintf("ASSET%d", i)
		s.markets[symbol] = MarketMetadata{Symbol: symbol, Asset: symbol, Status: "active", FundingInterval: time.Hour}
	}
	return s
}

func realisticBookFrame(depth int) []byte {
	var bids, asks []string
	for i := 0; i < depth; i++ {
		bids = append(bids, fmt.Sprintf("[%.6f,%.6f]", 150.25-float64(i)*0.001, 12.345678+float64(i)))
		asks = append(asks, fmt.Sprintf("[%.6f,%.6f]", 150.30+float64(i)*0.001, 23.456789+float64(i)))
	}
	return []byte(`{"channel":"orderbook","symbol":"ASSET0","orderbook":{"bids":[` + strings.Join(bids, ",") + `],"asks":[` + strings.Join(asks, ",") + `],"mid":150.275},"bypassExecutionBand":false}`)
}

func realisticStatsFrame() []byte {
	stats := make([]string, 100)
	for i := range stats {
		stats[i] = fmt.Sprintf(`{"symbol":"ASSET%d","timestamp":1793020205,"openInterest":65520.02,"markPrice":101.33,"midPrice":101.345,"oraclePrice":101.31,"currentFundingRate":0.02,"dayVolumeUsd":1234567.89}`, i)
	}
	return []byte(`{"channel":"marketStatsV2","stats":[` + strings.Join(stats, ",") + `]}`)
}

func BenchmarkMarketStreamApplyMessage(b *testing.B) {
	frames := map[string][]byte{"stats_100": realisticStatsFrame()}
	for _, depth := range []int{1, 20, 100, 500} {
		frames[fmt.Sprintf("book_%d", depth)] = realisticBookFrame(depth)
	}
	for name, raw := range frames {
		b.Run(name, func(b *testing.B) {
			s := decodeBenchmarkStream()
			b.ReportAllocs()
			b.SetBytes(int64(len(raw)))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := s.applyMessage(1, raw); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
