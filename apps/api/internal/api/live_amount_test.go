package api

import "testing"

type liveAmountTestAssetMap struct {
	decimals int
}

func (m liveAmountTestAssetMap) AssetIndex(string) (int, bool)   { return 213, true }
func (m liveAmountTestAssetMap) SizeDecimals(string) (int, bool) { return m.decimals, true }

type liveAmountTestLotSizes map[string]string

func (m liveAmountTestLotSizes) LotSize(symbol string) (string, bool) {
	value, ok := m[symbol]
	return value, ok
}

func TestNormalizeLiveHedgeAmountUsesPrecisionSupportedByBothVenues(t *testing.T) {
	server := &Server{live: &LiveDeps{
		hlAssetMap:       liveAmountTestAssetMap{decimals: 0},
		pacificaLotSizes: liveAmountTestLotSizes{"LIT": "0.1"},
	}}
	pacifica := legPlan{venue: "pacifica", symbol: "LIT"}
	hyperliquid := legPlan{venue: "hyperliquid", symbol: "LIT"}

	for _, legs := range [][2]legPlan{{pacifica, hyperliquid}, {hyperliquid, pacifica}} {
		amount, err := server.normalizeLiveHedgeAmount(7.5, legs[0], legs[1])
		if err != nil {
			t.Fatal(err)
		}
		if amount != 7 {
			t.Fatalf("normalized hedge amount = %v, want 7", amount)
		}
	}
}
