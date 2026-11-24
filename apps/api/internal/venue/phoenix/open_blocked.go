package phoenix

import (
	"context"
	"errors"
	"math"
	"math/big"
	"strconv"
	"strings"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
)

func (a *Adapter) OpenBlockedAssets() map[string]string {
	snapshot := a.stream.Snapshot()
	blocked := make(map[string]string)
	for _, market := range snapshot.Metadata.Markets {
		switch {
		case market.IsolatedOnly:
			blocked[market.Asset] = venue.OpenBlockIsolatedOnly
		case !market.OpeningPolicyKnown:
			blocked[market.Asset] = venue.OpenBlockDataUnavailable
		default:
			oi, known := market.OpenInterestBaseLots, market.HaveOpenInterest
			if stats, ok := snapshot.Stats[market.Symbol]; ok {
				var err error
				oi, err = baseLots(stats.OpenInterest, market.AmountIncrement)
				known = err == nil
			}
			if !known {
				blocked[market.Asset] = venue.OpenBlockDataUnavailable
			} else if oi >= market.OpenInterestCapBaseLots {
				blocked[market.Asset] = venue.OpenBlockOpenInterestCap
			}
		}
	}
	return blocked
}

// CheckOpenOrder reads a current REST snapshot once for the Phoenix leg. It is
// used only by live prepare, before either leg's signing requests are created.
func (a *Adapter) CheckOpenOrder(ctx context.Context, asset string, amount float64) (string, error) {
	metadata, err := a.stream.client.FetchExchangeMetadata(ctx)
	if err != nil {
		return "", err
	}
	for _, market := range metadata.Markets {
		if !strings.EqualFold(market.Asset, asset) {
			continue
		}
		if market.IsolatedOnly {
			return venue.OpenBlockIsolatedOnly, nil
		}
		if !market.OpeningPolicyKnown || !market.HaveOpenInterest || market.Status != "active" {
			return "", errors.New("Phoenix opening policy or open interest unavailable")
		}
		lots, err := baseLots(amount, market.AmountIncrement)
		if err != nil || lots == 0 {
			return "", errors.New("invalid Phoenix opening base lots")
		}
		// Phoenix rejects OrderOpenInterestCapExceeded; both catalog fields are base lots.
		// https://github.com/Ellipsis-Labs/rise-public/blob/f67e8b450d29a2c1031eb4669c6c70afefdab9fc/test-fixtures/transaction-errors.json#L1566-L1611
		// Subtract before comparing so an OI + order sum cannot overflow uint64.
		if market.OpenInterestBaseLots > market.OpenInterestCapBaseLots || lots > market.OpenInterestCapBaseLots-market.OpenInterestBaseLots {
			return venue.OpenBlockOpenInterestCap, nil
		}
		return "", nil
	}
	return "", errors.New("Phoenix opening market unavailable")
}

// baseLots converts a decimal base quantity without rounding or float arithmetic.
func baseLots(amount, increment float64) (uint64, error) {
	if amount < 0 || math.IsNaN(amount) || math.IsInf(amount, 0) || !positiveFinite(increment) {
		return 0, errors.New("invalid Phoenix base quantity")
	}
	quantity, _ := new(big.Rat).SetString(strconv.FormatFloat(amount, 'f', -1, 64))
	step, _ := new(big.Rat).SetString(strconv.FormatFloat(increment, 'f', -1, 64))
	lots := new(big.Rat).Quo(quantity, step)
	if !lots.IsInt() || !lots.Num().IsUint64() {
		return 0, errors.New("Phoenix quantity is not an exact base-lot amount")
	}
	return lots.Num().Uint64(), nil
}
