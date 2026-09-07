package venue

import (
	"fmt"
	"math"
	"reflect"
	"sort"
	"strings"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
)

type ClosePricePolicy string

const (
	ClosePriceFromFill      ClosePricePolicy = "fill"
	ClosePriceFromMarketBBO ClosePricePolicy = "market_bbo"
)

type LiveCapabilities struct {
	ClosePricePolicy      ClosePricePolicy
	MinimumRetryNotional  float64
	ConfirmLeverageChange bool
	ClientOrderLookup     bool
}

type LiveOrderParams struct {
	Account       string
	Signer        string
	Symbol        string
	Side          domain.Side
	Amount        float64
	Price         float64
	ClientOrderID string
}

type ReduceAction string

const (
	ReduceActionClose          ReduceAction = "close"
	ReduceActionUnwind         ReduceAction = "unwind"
	ReduceActionEmergencyClose ReduceAction = "emergency_close"
)

type LiveReduceParams struct {
	LiveOrderParams
	Action ReduceAction
}

type LiveLeverageParams struct {
	Account  string
	Signer   string
	Symbol   string
	Leverage int
}

// LiveModule contains the venue-specific rules needed by non-custodial live
// orchestration. Account streams own submission and fill reconciliation.
type LiveModule interface {
	Name() string
	Capabilities() LiveCapabilities
	NormalizeAmount(symbol string, amount float64) (float64, error)
	BuildOpen(LiveOrderParams) (*domain.SigningRequest, error)
	BuildReduce(LiveReduceParams) (*domain.SigningRequest, error)
	BuildLeverage(LiveLeverageParams) (*domain.SigningRequest, error)
}

type LiveModuleRegistry struct {
	modules map[string]LiveModule
	names   []string
}

func NewLiveModuleRegistry(modules ...LiveModule) (*LiveModuleRegistry, error) {
	registry := &LiveModuleRegistry{modules: make(map[string]LiveModule, len(modules))}
	for _, module := range modules {
		if liveModuleIsNil(module) {
			return nil, fmt.Errorf("live venue module must not be nil")
		}
		rawName := module.Name()
		name := strings.TrimSpace(rawName)
		if name == "" || name != rawName || name != strings.ToLower(name) {
			return nil, fmt.Errorf("invalid live venue module name %q", rawName)
		}
		if _, exists := registry.modules[name]; exists {
			return nil, fmt.Errorf("duplicate live venue module %q", name)
		}
		capabilities := module.Capabilities()
		if capabilities.ClosePricePolicy != ClosePriceFromFill && capabilities.ClosePricePolicy != ClosePriceFromMarketBBO {
			return nil, fmt.Errorf("invalid close price policy for live venue module %q", name)
		}
		if capabilities.MinimumRetryNotional < 0 || math.IsNaN(capabilities.MinimumRetryNotional) || math.IsInf(capabilities.MinimumRetryNotional, 0) {
			return nil, fmt.Errorf("invalid minimum retry notional for live venue module %q", name)
		}
		registry.modules[name] = module
		registry.names = append(registry.names, name)
	}
	sort.Strings(registry.names)
	return registry, nil
}

func liveModuleIsNil(module LiveModule) bool {
	if module == nil {
		return true
	}
	value := reflect.ValueOf(module)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func (r *LiveModuleRegistry) Module(name string) (LiveModule, bool) {
	if r == nil {
		return nil, false
	}
	module, ok := r.modules[strings.ToLower(strings.TrimSpace(name))]
	return module, ok
}

func (r *LiveModuleRegistry) Names() []string {
	if r == nil {
		return nil
	}
	return append([]string(nil), r.names...)
}
