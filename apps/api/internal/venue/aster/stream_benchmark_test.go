package aster

import (
	"encoding/json"
	"fmt"
	"testing"
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
