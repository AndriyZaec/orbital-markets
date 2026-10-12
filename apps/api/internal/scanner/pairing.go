package scanner

import (
	"fmt"
	"math"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
)

const maxPairPriceDeviation = 0.20

// PairingRejection describes a price-consistency failure, not proven token identity.
type PairingRejection struct {
	Asset     string    `json:"asset"`
	VenueA    string    `json:"venue_a"`
	VenueB    string    `json:"venue_b"`
	MarketA   string    `json:"market_a"`
	MarketB   string    `json:"market_b"`
	PriceA    float64   `json:"price_a"`
	PriceB    float64   `json:"price_b"`
	Deviation *float64  `json:"deviation"`
	Reason    string    `json:"reason"`
	CheckedAt time.Time `json:"checked_at"`
}

func (r *PairingRejection) Error() string {
	if r.Reason == "price_validation_unavailable" {
		return "fresh oracle prices are unavailable for this venue pair; refresh opportunity"
	}
	return "oracle prices differ by more than 20% for this venue pair; refresh opportunity"
}

func validatePairing(asset string, a, b venue.MarketData, now time.Time) *PairingRejection {
	rejection := &PairingRejection{
		Asset: asset, VenueA: a.Venue, VenueB: b.Venue,
		MarketA: a.MarketKey, MarketB: b.MarketKey,
		Reason: "price_validation_unavailable", CheckedAt: now.UTC(),
	}
	// Keep diagnostics JSON-safe even when a venue reports NaN/Infinity.
	if positiveFinite(a.IndexPrice) {
		rejection.PriceA = a.IndexPrice
	}
	if positiveFinite(b.IndexPrice) {
		rejection.PriceB = b.IndexPrice
	}
	if !freshIndexPrice(a, now) || !freshIndexPrice(b, now) {
		return rejection
	}
	low, high := min(a.IndexPrice, b.IndexPrice), max(a.IndexPrice, b.IndexPrice)
	// Comparing against the upper price avoids rounding an exact 20% boundary
	// above the threshold (e.g. 1 versus 1.2).
	if high <= low*(1+maxPairPriceDeviation) {
		return nil
	}
	rejection.Reason = "price_inconsistent"
	deviation := (high - low) / low
	if !math.IsInf(deviation, 0) && !math.IsNaN(deviation) {
		rejection.Deviation = &deviation
	}
	return rejection
}

func freshIndexPrice(md venue.MarketData, now time.Time) bool {
	if !positiveFinite(md.IndexPrice) {
		return false
	}
	timestamp := md.Timestamp
	if md.Health != nil {
		index := md.Health.Index
		if index.Status != venue.HealthAvailable {
			return false
		}
		timestamp = index.SourceTime
		if timestamp.IsZero() {
			timestamp = index.ReceivedAt
		}
	}
	return !timestamp.IsZero() && !timestamp.After(now) && now.Sub(timestamp) <= maxSnapshotAge
}

func pairID(asset, venueA, venueB string) string {
	return fmt.Sprintf("%s-%s-%s", asset, venueA, venueB)
}

func pairingRejections(byAsset map[string][]venue.MarketData, now time.Time) map[string]*PairingRejection {
	rejected := make(map[string]*PairingRejection)
	for asset, snapshots := range byAsset {
		for i := range snapshots {
			for j := i + 1; j < len(snapshots); j++ {
				a, b := canonicalVenuePair(snapshots[i], snapshots[j])
				if a.Venue == b.Venue {
					continue
				}
				if rejection := validatePairing(asset, a, b, now); rejection != nil {
					rejected[pairID(asset, a.Venue, b.Venue)] = rejection
				}
			}
		}
	}
	return rejected
}

// Log transitions only, rather than repeating the same rejection on every scan.
// This state is accessed only while scanMu is held and contains no history.
func (s *Scanner) logPairingRejections(rejected map[string]*PairingRejection) {
	reasons := make(map[string]string, len(rejected))
	for id, rejection := range rejected {
		reasons[id] = rejection.Reason
		if s.pairingReasons[id] == rejection.Reason {
			continue
		}
		fields := []any{
			"asset", rejection.Asset, "venue_a", rejection.VenueA, "venue_b", rejection.VenueB,
			"market_a", rejection.MarketA, "market_b", rejection.MarketB,
			"index_price_a", rejection.PriceA, "index_price_b", rejection.PriceB,
			"reason", rejection.Reason, "threshold_pct", maxPairPriceDeviation * 100,
		}
		if rejection.Deviation != nil {
			fields = append(fields, "deviation_pct", *rejection.Deviation*100)
		}
		s.logger.Warn("venue pair rejected", fields...)
	}
	s.pairingReasons = reasons
}
