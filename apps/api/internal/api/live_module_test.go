package api

import (
	"math"
	"testing"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
)

type fakeLiveModule struct {
	name       string
	open       venue.LiveOrderParams
	reduce     venue.LiveReduceParams
	normalized float64
}

func (m *fakeLiveModule) Name() string { return m.name }
func (*fakeLiveModule) Capabilities() venue.LiveCapabilities {
	return venue.LiveCapabilities{ClosePricePolicy: venue.ClosePriceFromFill}
}
func (m *fakeLiveModule) NormalizeAmount(string, float64) (float64, error) {
	return m.normalized, nil
}
func (m *fakeLiveModule) BuildOpen(params venue.LiveOrderParams) (*domain.SigningRequest, error) {
	m.open = params
	return &domain.SigningRequest{Venue: m.name, Account: params.Account, Signer: params.Signer}, nil
}
func (m *fakeLiveModule) BuildReduce(params venue.LiveReduceParams) (*domain.SigningRequest, error) {
	m.reduce = params
	return &domain.SigningRequest{Venue: m.name, Action: string(params.Action)}, nil
}
func (*fakeLiveModule) BuildLeverage(venue.LiveLeverageParams) (*domain.SigningRequest, error) {
	return &domain.SigningRequest{}, nil
}

func TestLiveSigningDispatchesThroughRegisteredModule(t *testing.T) {
	module := &fakeLiveModule{name: "alpha", normalized: 7}
	modules, err := venue.NewLiveModuleRegistry(module)
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{live: &LiveDeps{modules: modules}}
	leg := legPlan{venue: "alpha", symbol: "SOL", side: domain.SideLong, price: 100}
	bindings := liveVenueBindings{
		Accounts: map[string]string{"alpha": "owner"},
		Agents:   map[string]string{"alpha": "agent"},
	}

	amount, err := server.normalizeLiveHedgeAmount(7.5, leg)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(amount-7) > 1e-9 {
		t.Fatalf("normalized amount = %v, want 7", amount)
	}
	request, err := server.buildOpenSigningRequest(leg, amount, "open-id", bindings)
	if err != nil {
		t.Fatal(err)
	}
	if request.Venue != "alpha" || module.open.Account != "owner" || module.open.Signer != "agent" {
		t.Fatalf("open request = %+v, params = %+v", request, module.open)
	}
	if _, err := server.buildUnwindSigningRequest(leg, amount, "unwind-id", bindings); err != nil {
		t.Fatal(err)
	}
	if module.reduce.Action != venue.ReduceActionUnwind {
		t.Fatalf("reduce action = %q, want unwind", module.reduce.Action)
	}
}
