package account

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/helper"
)

type riskComputerFunc func(context.Context, helper.RiskRequest) (helper.RiskResult, error)

func (f riskComputerFunc) ComputeRisk(ctx context.Context, q helper.RiskRequest) (helper.RiskResult, error) {
	return f(ctx, q)
}

func riskResult(q helper.RiskRequest) helper.RiskResult {
	expires, _ := helper.RiskExpiry(q)
	liquidation := make(map[string]*float64)
	for _, p := range q.Positions {
		if p.BaseLots != "0" {
			liquidation[p.Symbol] = nil
		}
	}
	return helper.RiskResult{
		OwnerAuthority: q.OwnerAuthority, AccountGeneration: q.AccountGeneration, AccountSlot: q.AccountSlot, AccountSequence: q.AccountSequence,
		MetadataGeneration: q.MetadataGeneration, AsOfUnixMS: q.AsOfUnixMS, ValidUntilUnixMS: expires, QuoteDecimals: 6,
		EquityQuoteLots: "100", EffectiveCollateralQuoteLots: "100", AvailableMarginQuoteLots: "90", InitialMarginQuoteLots: "10", MaintenanceMarginQuoteLots: "5",
		UnsettledFundingQuoteLots: "-2", UnrealizedPnLQuoteLots: "0", SpotNotionalQuoteLots: "0", DiscountedSpotQuoteLots: "0",
		RiskTier: "Safe", LiquidationModel: "rise_local_static", StaticLiquidationPricesUSD: liquidation,
	}
}

func riskInputs(at time.Time) RiskInputs {
	return RiskInputs{MetadataGeneration: 7, ParamsObservedAt: at,
		Markets: []helper.RiskMarket{{Symbol: "BTC", PriceObservedUnixMS: at.UnixMilli()}, {Symbol: "SOL", PriceObservedUnixMS: at.UnixMilli()}}}
}

func TestRiskAdapterPreservesAllAccountInputsWithoutComputingMargin(t *testing.T) {
	s, _, at := readyState(t)
	s.mu.Lock()
	s.snapshot.Cross.Splines = nil
	s.mu.Unlock()
	computer := riskComputerFunc(func(_ context.Context, q helper.RiskRequest) (helper.RiskResult, error) {
		if q.OwnerAuthority != owner || q.CollateralQuoteLots != "9007199254740993" || len(q.Positions) != 1 ||
			q.Positions[0].BaseLots != "-9007199254740993" || q.Positions[0].UnsettledFundingQuoteLots != "-123" ||
			len(q.Orders) != 1 || q.Orders[0].Sequence != "18446744073709551615" || !q.Orders[0].ReduceOnly ||
			len(q.SpotBalances) != 1 || q.SpotBalances[0].Symbol != "SOL" || q.SpotBalances[0].Balance != "1000000001" {
			t.Fatal("risk adapter omitted or rounded account inputs")
		}
		return riskResult(q), nil
	})
	result, err := s.risk(context.Background(), computer, riskInputs(at), func() time.Time { return at.Add(time.Millisecond) })
	if err != nil || result.AvailableMarginQuoteLots != "90" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestRiskRejectsAccountChangeAndExpiryWhileHelperRuns(t *testing.T) {
	for _, mode := range []string{"delta", "disconnect", "expiry", "helper failure", "wrong result"} {
		t.Run(mode, func(t *testing.T) {
			s, generation, at := readyState(t)
			s.mu.Lock()
			s.snapshot.Cross.Splines = nil
			s.mu.Unlock()
			now := at.Add(time.Millisecond)
			computer := riskComputerFunc(func(_ context.Context, q helper.RiskRequest) (helper.RiskResult, error) {
				result := riskResult(q)
				switch mode {
				case "delta":
					if err := s.apply(generation, deltaMessage(t), at.Add(time.Millisecond), true); err != nil {
						t.Fatal(err)
					}
				case "disconnect":
					s.disconnect(generation)
				case "expiry":
					now = at.Add(31 * time.Second)
				case "helper failure":
					return helper.RiskResult{}, errors.New("unavailable")
				case "wrong result":
					result.AccountSequence++
				}
				return result, nil
			})
			result, err := s.risk(context.Background(), computer, riskInputs(at), func() time.Time { return now })
			if err == nil || result.OwnerAuthority != "" {
				t.Fatalf("returned risk on %s: %+v %v", mode, result, err)
			}
		})
	}
}

func TestRiskRejectsSplineAndStaleInputsBeforeCallingHelper(t *testing.T) {
	for _, mode := range []string{"splines", "stale account", "stale price", "missing params"} {
		t.Run(mode, func(t *testing.T) {
			s, _, at := readyState(t)
			if mode != "splines" {
				s.mu.Lock()
				s.snapshot.Cross.Splines = nil
				s.mu.Unlock()
			}
			now := at.Add(time.Millisecond)
			inputs := riskInputs(at)
			switch mode {
			case "stale account":
				now = at.Add(31 * time.Second)
			case "stale price":
				inputs.Markets[0].PriceObservedUnixMS -= 30001
			case "missing params":
				inputs.ParamsObservedAt = time.Time{}
			}
			computer := riskComputerFunc(func(_ context.Context, _ helper.RiskRequest) (helper.RiskResult, error) {
				t.Fatal("called helper with invalid inputs")
				return helper.RiskResult{}, nil
			})
			if _, err := s.risk(context.Background(), computer, inputs, func() time.Time { return now }); err == nil {
				t.Fatal("accepted incomplete or stale state")
			}
		})
	}
}
