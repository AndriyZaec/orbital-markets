package api

import (
	"testing"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
)

func TestClassifyOpportunitySignal(t *testing.T) {
	tests := []struct {
		name        string
		currentEdge float64
		direction   domain.Direction
		stats       signalStats
		want        string
	}{
		{name: "persistent", currentEdge: 0.20, direction: domain.DirectionLongA, stats: signalStats{Samples: 100, Active: 80, LongA: 70, LongB: 10}, want: "persistent"},
		{name: "intermittent", currentEdge: 0.20, direction: domain.DirectionLongA, stats: signalStats{Samples: 100, Active: 40, LongA: 40}, want: "intermittent"},
		{name: "new", currentEdge: 0.20, direction: domain.DirectionLongA, stats: signalStats{Samples: 100, Active: 10, LongA: 10}, want: "new"},
		{name: "choppy", currentEdge: 0.20, direction: domain.DirectionLongA, stats: signalStats{Samples: 100, Active: 50, LongA: 30, LongB: 20}, want: "choppy"},
		{name: "reversed", currentEdge: 0.20, direction: domain.DirectionLongA, stats: signalStats{Samples: 100, Active: 50, LongA: 10, LongB: 40}, want: "reversed"},
		{name: "faded", direction: domain.DirectionLongA, stats: signalStats{Samples: 100, Active: 40, LongA: 40}, want: "faded"},
		{name: "flat", direction: domain.DirectionLongA, stats: signalStats{Samples: 100, Active: 10, LongA: 10}, want: "flat"},
		{name: "limited", currentEdge: 0.20, direction: domain.DirectionLongA, stats: signalStats{Samples: 4, Active: 4, LongA: 4}, want: "limited"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opportunity := domain.Opportunity{AnnualizedGrossEdge: tt.currentEdge, Direction: tt.direction}
			if got := classifyOpportunitySignal(opportunity, tt.stats).Status; got != tt.want {
				t.Fatalf("status = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCalculateOpportunitySignalsPairsBucketsAndTracksDirection(t *testing.T) {
	opportunity := domain.Opportunity{
		ID:                  "SOL-pacifica-hyperliquid",
		Asset:               "SOL",
		VenuePair:           domain.VenuePair{VenueA: "pacifica", VenueB: "hyperliquid"},
		Direction:           domain.DirectionLongA,
		AnnualizedGrossEdge: 0.20,
	}
	rows := []signalFundingRow{
		{Venue: "pacifica", Asset: "SOL", BucketUnix: 1, FundingRate: 0},
		{Venue: "hyperliquid", Asset: "SOL", BucketUnix: 1, FundingRate: 0.0002},
		{Venue: "pacifica", Asset: "SOL", BucketUnix: 2, FundingRate: 0.0003},
		{Venue: "hyperliquid", Asset: "SOL", BucketUnix: 2, FundingRate: 0},
		{Venue: "pacifica", Asset: "SOL", BucketUnix: 3, FundingRate: 1},
	}

	signal := calculateOpportunitySignals([]domain.Opportunity{opportunity}, rows)[opportunity.ID]
	if signal.Samples != 2 {
		t.Fatalf("samples = %d, want 2 paired buckets", signal.Samples)
	}
	if signal.Activity != 1 || signal.DirectionConsistency != 0.5 {
		t.Fatalf("activity/consistency = %v/%v, want 1/0.5", signal.Activity, signal.DirectionConsistency)
	}
}
