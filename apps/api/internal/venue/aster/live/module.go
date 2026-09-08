package live

import (
	"fmt"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
)

type LiveModule struct {
	rules OrderRuleMap
}

func NewLiveModule(rules OrderRuleMap) *LiveModule {
	return &LiveModule{rules: rules}
}

func (m *LiveModule) Name() string {
	return "aster"
}

func (m *LiveModule) Capabilities() venue.LiveCapabilities {
	return venue.LiveCapabilities{
		ClosePricePolicy:      venue.ClosePriceFromMarketBBO,
		LeverageUpdate:        venue.LeverageUpdateRequired,
		ConfirmLeverageChange: true,
		ClientOrderLookup:     false,
	}
}

func (m *LiveModule) NormalizeAmount(symbol string, amount float64) (float64, error) {
	if m.rules == nil {
		return 0, fmt.Errorf("Aster order rules not configured")
	}
	rules, ok := m.rules.OrderRules(symbol)
	if !ok {
		return 0, fmt.Errorf("Aster order rules unavailable for symbol: %s", symbol)
	}
	normalized, _, err := normalizeQuantity(amount, rules)
	return normalized, err
}

func (m *LiveModule) BuildOpen(params venue.LiveOrderParams) (*domain.SigningRequest, error) {
	return BuildOpenPayload(
		m.rules, params.Account, params.Signer, params.Symbol, params.Side,
		params.Amount, params.Price, params.ClientOrderID, nil,
	)
}

func (m *LiveModule) BuildReduce(params venue.LiveReduceParams) (*domain.SigningRequest, error) {
	switch params.Action {
	case venue.ReduceActionClose:
		return BuildClosePayload(
			m.rules, params.Account, params.Signer, params.Symbol, params.Side,
			params.Amount, params.Price, params.ClientOrderID, nil,
		)
	case venue.ReduceActionUnwind:
		return BuildUnwindPayload(
			m.rules, params.Account, params.Signer, params.Symbol, params.Side,
			params.Amount, params.Price, params.ClientOrderID,
		)
	case venue.ReduceActionEmergencyClose:
		return BuildEmergencyClosePayload(
			m.rules, params.Account, params.Signer, params.Symbol, params.Side,
			params.Amount, params.Price, params.ClientOrderID,
		)
	default:
		return nil, fmt.Errorf("unsupported reduce action: %s", params.Action)
	}
}

func (m *LiveModule) BuildLeverage(params venue.LiveLeverageParams) (*domain.SigningRequest, error) {
	return BuildPrivatePayload(PrivateRequestParams{
		Operation: UpdateLeverage,
		User:      params.Account,
		Signer:    params.Signer,
		Symbol:    params.Symbol,
		Leverage:  params.Leverage,
	})
}
