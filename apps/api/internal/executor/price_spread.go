package executor

import (
	"math"
	"strings"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
)

// EntryPriceSpread compares quantity-weighted confirmed short and long fills.
func EntryPriceSpread(fills []LiveFill) *float64 {
	quantities := map[string]float64{}
	values := map[string]float64{}
	for _, fill := range fills {
		if !fill.Filled || fill.FilledAmount <= 0 {
			continue
		}
		side := strings.ToLower(fill.Side)
		if (side != "long" && side != "short") || fill.AvgFillPrice <= 0 ||
			math.IsNaN(fill.AvgFillPrice) || math.IsInf(fill.AvgFillPrice, 0) ||
			math.IsNaN(fill.FilledAmount) || math.IsInf(fill.FilledAmount, 0) {
			return nil
		}
		quantities[side] += fill.FilledAmount
		values[side] += fill.AvgFillPrice * fill.FilledAmount
	}
	return domain.MidpointPriceSpread(values["long"]/quantities["long"], values["short"]/quantities["short"])
}
