package live

import (
	"strings"
	"testing"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
)

func TestLiveModuleBuildsAsterSigningRequests(t *testing.T) {
	module := NewLiveModule(payloadTestRules, &BuilderConfig{Address: testBuilder, FeeRate: "0.0002"})
	params := venue.LiveOrderParams{
		Account: testUser, Signer: testSigner, Symbol: "BTCUSDT", Side: domain.SideLong,
		Amount: 2.1239, Price: 100, ClientOrderID: "client-id",
	}

	amount, err := module.NormalizeAmount(params.Symbol, params.Amount)
	if err != nil {
		t.Fatal(err)
	}
	if amount != 2.123 {
		t.Fatalf("normalized amount = %v, want 2.123", amount)
	}
	open, err := module.BuildOpen(params)
	if err != nil {
		t.Fatal(err)
	}
	if open.Venue != "aster" || open.Action != "open" || open.Account != testUser || open.Signer != testSigner {
		t.Fatalf("open request = %+v", open)
	}
	if !strings.Contains(string(open.UnsignedPayload), "builder="+testBuilder) ||
		!strings.Contains(string(open.UnsignedPayload), "feeRate=0.0002") {
		t.Fatalf("open request omits builder attribution: %s", open.UnsignedPayload)
	}
	for _, action := range []venue.ReduceAction{
		venue.ReduceActionClose, venue.ReduceActionUnwind, venue.ReduceActionEmergencyClose,
	} {
		request, err := module.BuildReduce(venue.LiveReduceParams{LiveOrderParams: params, Action: action})
		if err != nil {
			t.Fatal(err)
		}
		if request.Action != string(action) || !request.ReduceOnly {
			t.Fatalf("%s request = %+v", action, request)
		}
		hasBuilder := strings.Contains(string(request.UnsignedPayload), "builder="+testBuilder)
		if hasBuilder != (action == venue.ReduceActionClose) {
			t.Fatalf("%s builder attribution = %t", action, hasBuilder)
		}
	}
	leverage, err := module.BuildLeverage(venue.LiveLeverageParams{
		Account: testUser, Signer: testSigner, Symbol: "BTCUSDT", Leverage: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if leverage.Venue != "aster" || leverage.Action != string(UpdateLeverage) || leverage.Leverage != 3 {
		t.Fatalf("leverage request = %+v", leverage)
	}

	capabilities := module.Capabilities()
	if capabilities.ClosePricePolicy != venue.ClosePriceFromMarketBBO ||
		capabilities.LeverageUpdate != venue.LeverageUpdateRequired ||
		!capabilities.ConfirmLeverageChange || capabilities.ClientOrderLookup {
		t.Fatalf("capabilities = %+v", capabilities)
	}
}

func TestLiveModuleRejectsUnknownReduceAction(t *testing.T) {
	module := NewLiveModule(payloadTestRules, nil)
	_, err := module.BuildReduce(venue.LiveReduceParams{
		LiveOrderParams: venue.LiveOrderParams{
			Account: testUser, Signer: testSigner, Symbol: "BTCUSDT", Side: domain.SideLong,
			Amount: 1, Price: 100, ClientOrderID: "client-id",
		},
		Action: "invalid",
	})
	if err == nil {
		t.Fatal("unknown reduce action was accepted")
	}
}
