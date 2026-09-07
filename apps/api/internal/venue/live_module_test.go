package venue

import (
	"testing"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
)

type registryTestModule struct {
	name string
}

func (m registryTestModule) Name() string { return m.name }
func (registryTestModule) Capabilities() LiveCapabilities {
	return LiveCapabilities{ClosePricePolicy: ClosePriceFromFill, LeverageUpdate: LeverageUpdateNotRequired}
}

type registryMissingLeveragePolicyModule struct{ registryTestModule }

func (registryMissingLeveragePolicyModule) Capabilities() LiveCapabilities {
	return LiveCapabilities{ClosePricePolicy: ClosePriceFromFill}
}

type registryInvalidLeveragePolicyModule struct{ registryTestModule }

func (registryInvalidLeveragePolicyModule) Capabilities() LiveCapabilities {
	return LiveCapabilities{ClosePricePolicy: ClosePriceFromFill, LeverageUpdate: "sometimes"}
}
func (registryTestModule) NormalizeAmount(_ string, amount float64) (float64, error) {
	return amount, nil
}
func (registryTestModule) BuildOpen(LiveOrderParams) (*domain.SigningRequest, error) {
	return &domain.SigningRequest{}, nil
}
func (registryTestModule) BuildReduce(LiveReduceParams) (*domain.SigningRequest, error) {
	return &domain.SigningRequest{}, nil
}
func (registryTestModule) BuildLeverage(LiveLeverageParams) (*domain.SigningRequest, error) {
	return &domain.SigningRequest{}, nil
}

func TestLiveModuleRegistryIndexesModulesByNormalizedVenue(t *testing.T) {
	registry, err := NewLiveModuleRegistry(
		registryTestModule{name: "zeta"}, registryTestModule{name: "alpha"},
	)
	if err != nil {
		t.Fatal(err)
	}
	module, ok := registry.Module(" ALPHA ")
	if !ok || module.Name() != "alpha" {
		t.Fatalf("module = %v, found = %v", module, ok)
	}
	names := registry.Names()
	if len(names) != 2 || names[0] != "alpha" || names[1] != "zeta" {
		t.Fatalf("names = %v, want [alpha zeta]", names)
	}
	names[0] = "changed"
	if registry.Names()[0] != "alpha" {
		t.Fatal("Names returned mutable registry storage")
	}
}

func TestLiveModuleRegistryRejectsInvalidModules(t *testing.T) {
	var typedNil *registryTestModule
	tests := map[string][]LiveModule{
		"nil":                     {nil},
		"typed nil":               {typedNil},
		"empty name":              {registryTestModule{}},
		"mixed case":              {registryTestModule{name: "Alpha"}},
		"whitespace":              {registryTestModule{name: " alpha"}},
		"duplicate":               {registryTestModule{name: "alpha"}, registryTestModule{name: "alpha"}},
		"missing leverage policy": {registryMissingLeveragePolicyModule{registryTestModule{name: "alpha"}}},
		"invalid leverage policy": {registryInvalidLeveragePolicyModule{registryTestModule{name: "alpha"}}},
	}
	for name, modules := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := NewLiveModuleRegistry(modules...); err == nil {
				t.Fatal("expected registry construction error")
			}
		})
	}
}
