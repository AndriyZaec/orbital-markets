package live

import (
	"fmt"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
)

const minimumRetryNotional = 10.0

type LiveModule struct {
	assetMap AssetMap
}

func NewLiveModule(assetMap AssetMap) *LiveModule {
	return &LiveModule{assetMap: assetMap}
}

func (m *LiveModule) Name() string {
	return "hyperliquid"
}

func (m *LiveModule) Capabilities() venue.LiveCapabilities {
	return venue.LiveCapabilities{
		ClosePricePolicy:     venue.ClosePriceFromMarketBBO,
		MinimumRetryNotional: minimumRetryNotional,
		ClientOrderLookup:    true,
	}
}

func (m *LiveModule) NormalizeAmount(symbol string, amount float64) (float64, error) {
	if m.assetMap == nil {
		return 0, fmt.Errorf("hyperliquid asset map not configured")
	}
	return NormalizeAmount(m.assetMap, symbol, amount)
}

func (m *LiveModule) BuildOpen(params venue.LiveOrderParams) (*domain.SigningRequest, error) {
	if m.assetMap == nil {
		return nil, fmt.Errorf("hyperliquid asset map not configured")
	}
	request, err := BuildOpenPayload(
		m.assetMap, params.Symbol, params.Side, params.Amount,
		params.Price, params.ClientOrderID,
	)
	return bindLiveRequest(request, params.Account, params.Signer, err)
}

func (m *LiveModule) BuildReduce(params venue.LiveReduceParams) (*domain.SigningRequest, error) {
	if m.assetMap == nil {
		return nil, fmt.Errorf("hyperliquid asset map not configured")
	}
	var request *domain.SigningRequest
	var err error
	switch params.Action {
	case venue.ReduceActionClose:
		request, err = BuildClosePayload(m.assetMap, params.Symbol, params.Side, params.Amount, params.Price, params.ClientOrderID)
	case venue.ReduceActionUnwind:
		request, err = BuildUnwindPayload(m.assetMap, params.Symbol, params.Side, params.Amount, params.Price, params.ClientOrderID)
	case venue.ReduceActionEmergencyClose:
		request, err = BuildEmergencyClosePayload(m.assetMap, params.Symbol, params.Side, params.Amount, params.Price, params.ClientOrderID)
	default:
		return nil, fmt.Errorf("unsupported reduce action: %s", params.Action)
	}
	return bindLiveRequest(request, params.Account, params.Signer, err)
}

func (m *LiveModule) BuildLeverage(params venue.LiveLeverageParams) (*domain.SigningRequest, error) {
	if m.assetMap == nil {
		return nil, fmt.Errorf("hyperliquid asset map not configured")
	}
	request, err := BuildUpdateLeveragePayload(m.assetMap, params.Account, params.Symbol, params.Leverage)
	return bindLiveRequest(request, params.Account, params.Signer, err)
}

func bindLiveRequest(request *domain.SigningRequest, account, signer string, err error) (*domain.SigningRequest, error) {
	if err != nil {
		return nil, err
	}
	if signer == "" {
		return nil, fmt.Errorf("hyperliquid trading agent required")
	}
	request.Account = account
	request.Signer = signer
	return request, nil
}
