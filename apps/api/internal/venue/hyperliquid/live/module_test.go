package live

import (
	"testing"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
)

func TestLiveModuleBuildsBoundSigningRequests(t *testing.T) {
	module := NewLiveModule(payloadTestAssetMap{index: 7, decimals: 3})
	params := venue.LiveOrderParams{
		Account: "0xowner", Signer: "0xagent", Symbol: "VIRTUAL", Side: domain.SideLong,
		Amount: 20.123, Price: 1.25, ClientOrderID: "client-id",
	}
	open, err := module.BuildOpen(params)
	if err != nil {
		t.Fatal(err)
	}
	if open.Venue != "hyperliquid" || open.Action != "open" || open.Account != "0xowner" || open.Signer != "0xagent" {
		t.Fatalf("open request = %+v", open)
	}
	for _, action := range []venue.ReduceAction{
		venue.ReduceActionClose, venue.ReduceActionUnwind, venue.ReduceActionEmergencyClose,
	} {
		request, err := module.BuildReduce(venue.LiveReduceParams{LiveOrderParams: params, Action: action})
		if err != nil {
			t.Fatal(err)
		}
		if request.Action != string(action) || request.Account != "0xowner" || request.Signer != "0xagent" {
			t.Fatalf("%s request = %+v", action, request)
		}
	}
	leverage, err := module.BuildLeverage(venue.LiveLeverageParams{
		Account: "0xowner", Signer: "0xagent", Symbol: "VIRTUAL", Leverage: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if leverage.Action != "update_leverage" || leverage.Account != "0xowner" || leverage.Signer != "0xagent" {
		t.Fatalf("leverage request = %+v", leverage)
	}
	capabilities := module.Capabilities()
	if capabilities.ClosePricePolicy != venue.ClosePriceFromMarketBBO || capabilities.MinimumRetryNotional != 10 || !capabilities.ClientOrderLookup {
		t.Fatalf("capabilities = %+v", capabilities)
	}
}
