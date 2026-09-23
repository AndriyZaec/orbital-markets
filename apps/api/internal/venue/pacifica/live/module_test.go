package live

import (
	"testing"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
)

func TestLiveModuleBuildsBoundSigningRequests(t *testing.T) {
	module := NewLiveModule(payloadTestLotSizes)
	params := venue.LiveOrderParams{
		Account: "owner", Signer: "agent", Symbol: "SOL", Side: domain.SideLong,
		Amount: 2.75, Price: 100, ClientOrderID: "client-id",
	}
	open, err := module.BuildOpen(params)
	if err != nil {
		t.Fatal(err)
	}
	if open.Venue != "pacifica" || open.Action != "open" || open.Account != "owner" || open.Signer != "agent" {
		t.Fatalf("open request = %+v", open)
	}
	for _, action := range []venue.ReduceAction{
		venue.ReduceActionClose, venue.ReduceActionUnwind, venue.ReduceActionEmergencyClose,
	} {
		request, err := module.BuildReduce(venue.LiveReduceParams{LiveOrderParams: params, Action: action})
		if err != nil {
			t.Fatal(err)
		}
		if request.Action != string(action) || request.Account != "owner" || request.Signer != "agent" {
			t.Fatalf("%s request = %+v", action, request)
		}
	}
	leverage, err := module.BuildLeverage(venue.LiveLeverageParams{
		Account: "owner", Signer: "agent", Symbol: "SOL", Leverage: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if leverage.Action != "update_leverage" || leverage.Account != "owner" || leverage.Signer != "agent" {
		t.Fatalf("leverage request = %+v", leverage)
	}
	capabilities := module.Capabilities()
	if capabilities.ClosePricePolicy != venue.ClosePriceFromFill || capabilities.LeverageUpdate != venue.LeverageUpdateRequired || !capabilities.ConfirmLeverageChange || !capabilities.ClientOrderLookup {
		t.Fatalf("capabilities = %+v", capabilities)
	}
}
