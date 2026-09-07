package live

import (
	"fmt"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
)

type LiveModule struct {
	lotSizes LotSizeMap
}

func NewLiveModule(lotSizes LotSizeMap) *LiveModule {
	return &LiveModule{lotSizes: lotSizes}
}

func (m *LiveModule) Name() string {
	return "pacifica"
}

func (m *LiveModule) Capabilities() venue.LiveCapabilities {
	return venue.LiveCapabilities{
		ClosePricePolicy:      venue.ClosePriceFromFill,
		ConfirmLeverageChange: true,
		ClientOrderLookup:     true,
	}
}

func (m *LiveModule) NormalizeAmount(symbol string, amount float64) (float64, error) {
	return NormalizeAmount(m.lotSizes, symbol, amount)
}

func (m *LiveModule) BuildOpen(params venue.LiveOrderParams) (*domain.SigningRequest, error) {
	request, err := BuildOpenPayload(
		m.lotSizes, params.Account, params.Symbol, params.Side,
		params.Amount, params.Price, params.ClientOrderID,
	)
	return bindLiveRequest(request, params.Account, params.Signer, err)
}

func (m *LiveModule) BuildReduce(params venue.LiveReduceParams) (*domain.SigningRequest, error) {
	var request *domain.SigningRequest
	var err error
	switch params.Action {
	case venue.ReduceActionClose:
		request, err = BuildClosePayload(m.lotSizes, params.Account, params.Symbol, params.Side, params.Amount, params.Price, params.ClientOrderID)
	case venue.ReduceActionUnwind:
		request, err = BuildUnwindPayload(m.lotSizes, params.Account, params.Symbol, params.Side, params.Amount, params.Price, params.ClientOrderID)
	case venue.ReduceActionEmergencyClose:
		request, err = BuildEmergencyClosePayload(m.lotSizes, params.Account, params.Symbol, params.Side, params.Amount, params.Price, params.ClientOrderID)
	default:
		return nil, fmt.Errorf("unsupported reduce action: %s", params.Action)
	}
	return bindLiveRequest(request, params.Account, params.Signer, err)
}

func (m *LiveModule) BuildLeverage(params venue.LiveLeverageParams) (*domain.SigningRequest, error) {
	request, err := BuildUpdateLeveragePayload(params.Account, params.Symbol, params.Leverage)
	return bindLiveRequest(request, params.Account, params.Signer, err)
}

func bindLiveRequest(request *domain.SigningRequest, account, signer string, err error) (*domain.SigningRequest, error) {
	if err != nil {
		return nil, err
	}
	if signer == "" {
		return nil, fmt.Errorf("pacifica trading agent required")
	}
	request.Account = account
	request.Signer = signer
	return request, nil
}
