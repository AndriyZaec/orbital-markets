package executor

import (
	"math"
	"testing"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
)

func TestLegPricePnLReturnsQuoteCurrencyValue(t *testing.T) {
	tests := []struct {
		name     string
		side     domain.Side
		entry    float64
		mark     float64
		amount   float64
		expected float64
	}{
		{name: "long loss", side: domain.SideLong, entry: 100, mark: 99.30, amount: 2, expected: -1.40},
		{name: "short gain", side: domain.SideShort, entry: 100, mark: 99.325, amount: 2, expected: 1.35},
	}

	var total float64
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := legPricePnL(test.side, test.entry, test.mark, test.amount)
			if math.Abs(got-test.expected) > 1e-9 {
				t.Fatalf("price PnL = %v, want %v", got, test.expected)
			}
			total += got
		})
	}
	if math.Abs(total-(-0.05)) > 1e-9 {
		t.Fatalf("net price PnL = %v, want -0.05", total)
	}
}
