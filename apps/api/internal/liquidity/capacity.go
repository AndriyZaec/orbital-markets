// Package liquidity projects read-only L2 capacity for a selected opportunity.
// Execution sizing and recommended order sizes remain owned by scanner.
package liquidity

import (
	"math"
	"time"
)

const PairImpactBudget = 0.002 // 0.2% total depth impact, excluding spread/fees
const maxBookAge = 5 * time.Second
const maxBookSkew = 2 * time.Second

type level struct{ price, quantity float64 }
type book struct {
	bids, asks []level
	at         time.Time
}

type Estimate struct {
	Notional    float64
	BookLimited bool
	ObservedAt  time.Time
}

func finitePositive(n float64) bool { return n > 0 && !math.IsNaN(n) && !math.IsInf(n, 0) }

func validSide(side []level, bids bool) bool {
	if len(side) == 0 || len(side) > 50 {
		return false
	}
	for i, row := range side {
		if !finitePositive(row.price) || !finitePositive(row.quantity) {
			return false
		}
		if i > 0 && ((bids && row.price >= side[i-1].price) || (!bids && row.price <= side[i-1].price)) {
			return false
		}
	}
	return true
}

func validBook(b book, now time.Time) bool {
	return !b.at.IsZero() && !b.at.After(now.Add(time.Second)) && now.Sub(b.at) <= maxBookAge &&
		validSide(b.bids, true) && validSide(b.asks, false) && b.bids[0].price < b.asks[0].price
}

func quantity(side []level) float64 {
	var total float64
	for _, row := range side {
		total += row.quantity
	}
	return total
}

func vwap(side []level, amount float64) float64 {
	remaining, cost := amount, 0.0
	for _, row := range side {
		taken := min(remaining, row.quantity)
		cost += taken * row.price
		remaining -= taken
		if remaining <= amount*1e-12 {
			return cost / amount
		}
	}
	return math.Inf(1) // never extrapolate past visible levels
}

func capacity(long, short book, now time.Time) (Estimate, bool) {
	if !validBook(long, now) || !validBook(short, now) || long.at.Sub(short.at).Abs() > maxBookSkew {
		return Estimate{}, false
	}
	asks, bids := long.asks, short.bids
	ceiling := min(quantity(asks), quantity(bids))
	if !finitePositive(ceiling) {
		return Estimate{}, false
	}
	impact := func(q float64) float64 {
		return max(0, vwap(asks, q)/asks[0].price-1) + max(0, 1-vwap(bids, q)/bids[0].price)
	}
	amount := ceiling
	bookLimited := impact(ceiling) <= PairImpactBudget
	if !bookLimited {
		low, high := min(asks[0].quantity, bids[0].quantity), ceiling
		for range 60 {
			middle := (low + high) / 2
			if impact(middle) <= PairImpactBudget {
				low = middle
			} else {
				high = middle
			}
		}
		amount = low
	}
	// Both legs use the same native base quantity. Express table capacity as a
	// conservative per-leg quote notional at the lower best executable price.
	notional := amount * min(asks[0].price, bids[0].price)
	if !finitePositive(notional) {
		return Estimate{}, false
	}
	observed := long.at
	if short.at.Before(observed) {
		observed = short.at
	}
	return Estimate{Notional: notional, BookLimited: bookLimited, ObservedAt: observed}, true
}
