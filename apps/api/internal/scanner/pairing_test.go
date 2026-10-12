package scanner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
)

func TestPairingPriceConsistency(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name   string
		priceA float64
		priceB float64
		reason string
	}{
		{"same price", 1, 1, ""},
		{"below threshold", 1, 1.19, ""},
		{"exact threshold", 1, 1.2, ""},
		{"above threshold", 1, 1.20001, "price_inconsistent"},
		{"observed MEME", 0.000572, 0.01278734, "price_inconsistent"},
		{"zero", 0, 1, "price_validation_unavailable"},
		{"negative", -1, 1, "price_validation_unavailable"},
		{"nan", math.NaN(), 1, "price_validation_unavailable"},
		{"infinity", math.Inf(1), 1, "price_validation_unavailable"},
		{"overflow deviation", math.SmallestNonzeroFloat64, math.MaxFloat64, "price_inconsistent"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			a, b := healthyMarket("aster", "MEME", 0.001, now), healthyMarket("hyperliquid", "MEME", -0.001, now)
			a.IndexPrice, b.IndexPrice = test.priceA, test.priceB
			for _, pair := range [][2]venue.MarketData{{a, b}, {b, a}} {
				rejection := validatePairing("MEME", pair[0], pair[1], now)
				if test.reason == "" {
					if rejection != nil {
						t.Fatalf("valid pair rejected: %+v", rejection)
					}
				} else {
					if rejection == nil || rejection.Reason != test.reason {
						t.Fatalf("rejection = %+v, want %s", rejection, test.reason)
					}
					if _, err := json.Marshal(rejection); err != nil {
						t.Fatalf("diagnostic is not JSON-safe: %v", err)
					}
				}
			}
		})
	}
}

func TestPairingRequiresFreshIndexComponentNotFreshBook(t *testing.T) {
	now := time.Now()
	for _, test := range []struct {
		name       string
		status     venue.HealthStatus
		sourceTime time.Time
		receivedAt time.Time
		allowed    bool
	}{
		{"fresh source", venue.HealthAvailable, now, now, true},
		{"exact age limit", venue.HealthAvailable, now.Add(-maxSnapshotAge), now, true},
		{"stale source fresh reception", venue.HealthAvailable, now.Add(-time.Minute), now, false},
		{"received fallback", venue.HealthAvailable, time.Time{}, now, true},
		{"missing timestamps", venue.HealthAvailable, time.Time{}, time.Time{}, false},
		{"unavailable", venue.HealthUnavailable, now, now, false},
		{"future source", venue.HealthAvailable, now.Add(time.Minute), now, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			a, b := healthyMarket("aster", "MEME", 0.001, now), healthyMarket("hyperliquid", "MEME", -0.001, now)
			a.Health = &venue.MarketHealth{Index: venue.ComponentHealth{Status: test.status, SourceTime: test.sourceTime, ReceivedAt: test.receivedAt}}
			a.MarkPrice, a.BidPrice, a.AskPrice = 22, 21, 23
			if rejection := validatePairing("MEME", a, b, now); (rejection == nil) != test.allowed {
				t.Fatalf("rejection = %+v, allowed=%t", rejection, test.allowed)
			}
		})
	}
	a := healthyMarket("aster", "MEME", 0, now)
	a.Timestamp = time.Time{}
	if freshIndexPrice(a, now) {
		t.Fatal("legacy snapshot with unknown freshness passed")
	}
}

func TestScanHidesRejectedPairAndRecoversWithoutDeletingVenueData(t *testing.T) {
	now := time.Now()
	a := &mutableScannerAdapter{name: "aster", data: []venue.MarketData{healthyMarket("aster", "MEME", 0.001, now)}}
	b := &mutableScannerAdapter{name: "hyperliquid", data: []venue.MarketData{healthyMarket("hyperliquid", "MEME", -0.001, now)}}
	var logs bytes.Buffer
	s := New(slog.New(slog.NewJSONHandler(&logs, nil)), a, b)
	s.scan(context.Background())
	if len(s.Opportunities()) != 1 {
		t.Fatal("matching pair missing")
	}
	a.data[0].IndexPrice = 22
	s.scan(context.Background())
	if len(s.Opportunities()) != 0 {
		t.Fatal("previous opportunity was retained after price rejection")
	}
	if !strings.Contains(logs.String(), `"reason":"price_inconsistent"`) || !strings.Contains(logs.String(), `"threshold_pct":20`) {
		t.Fatalf("rejection details missing from logs: %s", logs.String())
	}
	if len(s.MarketData(context.Background())) != 2 {
		t.Fatal("pair rejection removed per-venue data from recorder input")
	}
	s.scan(context.Background())
	if strings.Count(logs.String(), `"msg":"venue pair rejected"`) != 1 {
		t.Fatal("unchanged rejection was logged repeatedly")
	}
	a.data[0].IndexPrice = 1
	s.scan(context.Background())
	if len(s.Opportunities()) != 1 {
		t.Fatal("matching pair did not recover")
	}
	a.data[0].Health = &venue.MarketHealth{Index: venue.ComponentHealth{Status: venue.HealthUnavailable}}
	s.scan(context.Background())
	if len(s.Opportunities()) != 0 || !strings.Contains(logs.String(), `"reason":"price_validation_unavailable"`) {
		t.Fatal("unavailable index component was published")
	}
}

func TestOpenPlanRevalidatesPricesAfterDiscovery(t *testing.T) {
	now := time.Now()
	a := &mutableScannerAdapter{name: "aster", data: []venue.MarketData{healthyMarket("aster", "MEME", 0.001, now)}}
	b := &mutableScannerAdapter{name: "hyperliquid", data: []venue.MarketData{healthyMarket("hyperliquid", "MEME", -0.001, now)}}
	s := New(slog.New(slog.NewTextHandler(io.Discard, nil)), a, b)
	s.scan(context.Background())
	opp := requireSingleOpportunity(t, s)
	if _, err := s.BuildPlan(context.Background(), opp.ID, 1, 100); err != nil {
		t.Fatalf("initial plan: %v", err)
	}
	b.data[0].IndexPrice = 0.01
	_, err := s.BuildPlan(context.Background(), opp.ID, 1, 100)
	var rejection *PairingRejection
	if !errors.As(err, &rejection) || rejection.Reason != "price_inconsistent" {
		t.Fatalf("plan error = %v, want price rejection", err)
	}
}
