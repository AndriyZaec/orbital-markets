package scanner

import (
	"context"
	"io"
	"log/slog"
	"math"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
)

func TestScanAcceptsRealZeroFundingAndRejectsNonFiniteFunding(t *testing.T) {
	for _, tc := range []struct {
		name  string
		rate  float64
		valid bool
	}{
		{"zero", 0, true},
		{"negative", -0.0001, true},
		{"NaN", math.NaN(), false},
		{"positive infinity", math.Inf(1), false},
		{"negative infinity", math.Inf(-1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Now()
			left := healthyMarket("hyperliquid", "SOL", tc.rate, now)
			right := healthyMarket("pacifica", "SOL", 0.0002, now)
			s := New(slog.New(slog.NewTextHandler(io.Discard, nil)),
				&mutableScannerAdapter{name: left.Venue, data: []venue.MarketData{left}},
				&mutableScannerAdapter{name: right.Venue, data: []venue.MarketData{right}},
			)
			s.scan(context.Background())
			if !tc.valid {
				if opportunities := s.Opportunities(); len(opportunities) != 0 {
					t.Fatalf("non-finite funding published an opportunity: %+v", opportunities)
				}
				if snapshots := s.MarketData(context.Background()); len(snapshots) != 1 || snapshots[0].Venue != right.Venue {
					t.Fatalf("non-finite funding leaked into API/history snapshots: %+v", snapshots)
				}
				if got := classifyConfidence(left, right, now); got != domain.ConfidenceLow {
					t.Fatalf("non-finite funding confidence = %s", got)
				}
				// Closing can still read a valid price independently of funding availability.
				if _, err := s.MarketSnapshot(context.Background(), left.Venue, left.Asset); err != nil {
					t.Fatalf("unknown funding blocked price lookup: %v", err)
				}
				return
			}
			opportunity := requireSingleOpportunity(t, s)
			if opportunity.Confidence != domain.ConfidenceHigh || opportunity.ExecutionStatus != "executable" {
				t.Fatalf("valid funding pair not executable with high confidence: %+v", opportunity)
			}
			if snapshots := s.MarketData(context.Background()); len(snapshots) != 2 {
				t.Fatalf("valid funding filtered from API/history: %+v", snapshots)
			}
		})
	}
}
