package api

import (
	"math"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
)

type fakeLiveModule struct {
	name         string
	capabilities venue.LiveCapabilities
	open         venue.LiveOrderParams
	reduce       venue.LiveReduceParams
	leverage     venue.LiveLeverageParams
	normalized   float64
}

func (m *fakeLiveModule) Name() string { return m.name }
func (m *fakeLiveModule) Capabilities() venue.LiveCapabilities {
	capabilities := m.capabilities
	if capabilities.ClosePricePolicy == "" {
		capabilities.ClosePricePolicy = venue.ClosePriceFromFill
	}
	if capabilities.LeverageUpdate == "" {
		capabilities.LeverageUpdate = venue.LeverageUpdateNotRequired
	}
	return capabilities
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
func (m *fakeLiveModule) BuildLeverage(params venue.LiveLeverageParams) (*domain.SigningRequest, error) {
	m.leverage = params
	return &domain.SigningRequest{Venue: m.name, Account: params.Account, Signer: params.Signer, Leverage: params.Leverage}, nil
}

func TestLiveLeverageRequestsFollowModuleCapabilities(t *testing.T) {
	alpha := &fakeLiveModule{name: "alpha", capabilities: venue.LiveCapabilities{
		ClosePricePolicy: venue.ClosePriceFromFill, LeverageUpdate: venue.LeverageUpdateRequired,
	}}
	beta := &fakeLiveModule{name: "beta"}
	modules, err := venue.NewLiveModuleRegistry(alpha, beta)
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{live: &LiveDeps{modules: modules}}
	bindings := liveVenueBindings{
		Accounts: map[string]string{"alpha": "alpha-owner", "beta": "beta-owner"},
		Agents:   map[string]string{"alpha": "alpha-agent", "beta": "beta-agent"},
	}

	requests, err := server.buildLeverageSigningRequests([]string{"alpha", "beta"}, bindings, "SOL", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(requests) != 1 || requests["alpha"] == nil || requests["beta"] != nil {
		t.Fatalf("leverage requests = %+v", requests)
	}
	if alpha.leverage.Account != "alpha-owner" || alpha.leverage.Signer != "alpha-agent" || alpha.leverage.Leverage != 3 {
		t.Fatalf("alpha leverage params = %+v", alpha.leverage)
	}
}

func TestLiveSessionClaimsArbitraryVenueBindings(t *testing.T) {
	manager := NewSessionManager()
	session := &LiveSession{
		ID:   "session-1",
		Leg1: legPlan{venue: "alpha"},
		Leg2: legPlan{venue: "beta"},
		Bindings: liveVenueBindings{Accounts: map[string]string{
			"alpha": "alpha-owner", "beta": "beta-owner",
		}},
		CreatedAt: time.Now(),
	}
	manager.put(session)

	if _, found, claimed := manager.claimForAccounts(session.ID, map[string]string{
		"alpha": "alpha-owner", "beta": "other-owner",
	}); found || claimed {
		t.Fatal("mismatched venue bindings claimed the session")
	}
	if _, found, claimed := manager.claimForAccounts(session.ID, map[string]string{
		"alpha": "alpha-owner", "beta": "beta-owner",
	}); !found || !claimed {
		t.Fatal("matching arbitrary venue bindings did not claim the session")
	}
}

func TestLivePlanVenuesPreserveArbitraryPair(t *testing.T) {
	venues := livePlanVenues(&domain.ExecutionPlan{
		Leg1: domain.Leg{Venue: " Alpha "},
		Leg2: domain.Leg{Venue: "BETA"},
	})
	if len(venues) != 2 || venues[0] != "alpha" || venues[1] != "beta" {
		t.Fatalf("plan venues = %v, want [alpha beta]", venues)
	}
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
