package account

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/helper"
)

type RiskComputer interface {
	ComputeRisk(context.Context, helper.RiskRequest) (helper.RiskResult, error)
}

// RiskInputs is a complete, immutable capture of venue parameters and prices
// for every position, order and nonzero spot collateral pricing market.
// Scanner metadata alone is deliberately insufficient to construct this view.
type RiskInputs struct {
	MetadataGeneration uint64
	ParamsObservedAt   time.Time
	Markets            []helper.RiskMarket
	SpotParams         []helper.RiskSpotParams
}

func (s *State) Risk(ctx context.Context, computer RiskComputer, inputs RiskInputs) (helper.RiskResult, error) {
	return s.risk(ctx, computer, inputs, time.Now)
}

func (s *State) risk(ctx context.Context, computer RiskComputer, inputs RiskInputs, now func() time.Time) (helper.RiskResult, error) {
	if computer == nil {
		return helper.RiskResult{}, errors.New("Phoenix risk helper unavailable")
	}
	before := s.Snapshot()
	at := now()
	if !before.Fresh(at) {
		return helper.RiskResult{}, errors.New("Phoenix account state is not fresh and synchronized")
	}
	q := helper.RiskRequest{
		OwnerAuthority: before.Owner, AccountGeneration: before.Generation, AccountSlot: before.Slot, AccountSequence: before.Sequence,
		MetadataGeneration: inputs.MetadataGeneration, AsOfUnixMS: at.UnixMilli(), AccountObservedUnixMS: before.ObservedAt.UnixMilli(),
		ParamsObservedUnixMS: inputs.ParamsObservedAt.UnixMilli(), CollateralQuoteLots: before.Cross.Collateral, HasSplines: len(before.Cross.Splines) != 0,
		Positions: []helper.RiskPosition{}, Orders: []helper.RiskOrder{}, SpotBalances: []helper.RiskSpotBalance{},
		Markets: append([]helper.RiskMarket{}, inputs.Markets...), SpotParams: append([]helper.RiskSpotParams{}, inputs.SpotParams...),
	}
	if q.HasSplines {
		return helper.RiskResult{}, errors.New("Phoenix spline exposure requires Hawkeye risk views")
	}
	for symbol, position := range before.Cross.Positions {
		q.Positions = append(q.Positions, helper.RiskPosition{Symbol: symbol, Sequence: position.PositionSequenceNumber,
			BaseLots: position.BasePositionLots, VirtualQuoteLots: position.VirtualQuotePositionLots,
			UnsettledFundingQuoteLots: position.UnsettledFundingQuoteLots, AccumulatedFundingQuoteLots: position.AccumulatedFundingQuoteLots})
	}
	for symbol, orders := range before.Cross.Orders {
		for _, order := range orders {
			q.Orders = append(q.Orders, helper.RiskOrder{Symbol: symbol, Sequence: order.OrderSequenceNumber, Side: order.Side,
				PriceTicks: order.PriceTicks, RemainingBaseLots: order.SizeRemainingLots, InitialBaseLots: order.InitialSizeLots,
				ReduceOnly: order.ReduceOnly, IsStopLoss: order.IsStopLoss})
		}
	}
	for _, spot := range before.Cross.SpotCollaterals {
		q.SpotBalances = append(q.SpotBalances, helper.RiskSpotBalance{AssetIndex: spot.AssetIndex, Symbol: spot.Symbol, Balance: spot.Balance, Decimals: spot.Decimals})
	}
	// Stable ordering keeps fixed input captures reproducible across Go map iteration.
	sort.Slice(q.Positions, func(i, j int) bool { return q.Positions[i].Symbol < q.Positions[j].Symbol })
	sort.Slice(q.Orders, func(i, j int) bool {
		if q.Orders[i].Symbol != q.Orders[j].Symbol {
			return q.Orders[i].Symbol < q.Orders[j].Symbol
		}
		return q.Orders[i].Sequence < q.Orders[j].Sequence
	})
	if _, err := helper.RiskExpiry(q); err != nil {
		return helper.RiskResult{}, err
	}
	result, err := computer.ComputeRisk(ctx, q)
	if err != nil {
		return helper.RiskResult{}, err
	}
	after := s.Snapshot()
	at = now()
	if !after.Fresh(at) || after.Generation != before.Generation || after.Slot != before.Slot || after.Sequence != before.Sequence {
		return helper.RiskResult{}, errors.New("Phoenix account changed during risk computation")
	}
	if err := helper.ValidateRiskResult(q, result, at); err != nil {
		return helper.RiskResult{}, err
	}
	return result, nil
}
