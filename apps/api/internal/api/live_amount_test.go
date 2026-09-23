package api

import (
	"testing"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
	hllive "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/hyperliquid/live"
	paclive "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/pacifica/live"
)

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
	modules, err := venue.NewLiveModuleRegistry(
		paclive.NewLiveModule(liveAmountTestLotSizes{"LIT": "0.1"}),
		hllive.NewLiveModule(liveAmountTestAssetMap{decimals: 0}),
	)
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{live: &LiveDeps{modules: modules}}
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
