package domain

import "math"

// MidpointPriceSpread is positive when price B is above price A. The symmetric
// denominator allows reversing the pair by changing only the sign.
// Missing or invalid prices have no spread, rather than a fabricated zero.
func MidpointPriceSpread(a, b float64) *float64 {
	if a <= 0 || b <= 0 || math.IsNaN(a) || math.IsNaN(b) || math.IsInf(a, 0) || math.IsInf(b, 0) {
		return nil
	}
	spread := (b - a) / (a/2 + b/2)
	if math.IsNaN(spread) || math.IsInf(spread, 0) {
		return nil
	}
	return &spread
}
