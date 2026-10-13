package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/scanner"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
)

type prepareMarketAdapter struct {
	mu   sync.RWMutex
	data venue.MarketData
}

func (a *prepareMarketAdapter) Name() string { return a.data.Venue }
func (a *prepareMarketAdapter) FetchMarketData(context.Context) ([]venue.MarketData, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return []venue.MarketData{a.data}, nil
}

func TestPrepareExplainsBookAgingAfterExecutablePreview(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	now := time.Now()
	market := func(name string, rate float64) *prepareMarketAdapter {
		return &prepareMarketAdapter{data: venue.MarketData{
			Venue: name, Asset: "BTC", MarketKey: "BTC", MarkPrice: 100, IndexPrice: 100,
			BidPrice: 100, AskPrice: 100, BidSize: 100, AskSize: 100,
			OpenInterest: 1000, MaxLeverage: 10, FundingRate: rate, Timestamp: now,
			Health: &venue.MarketHealth{
				Index: venue.ComponentHealth{Status: venue.HealthAvailable, SourceTime: now},
				Book:  venue.ComponentHealth{Status: venue.HealthAvailable, SourceTime: now},
			},
		}}
	}
	long, short := market("pacifica", 0.000001), market("hyperliquid", 0.000006)
	sc := scanner.New(logger, long, short)
	go sc.Run(ctx, time.Hour)
	deadline := time.Now().Add(time.Second)
	for len(sc.Opportunities()) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(sc.Opportunities()) == 0 {
		t.Fatal("no opportunity")
	}
	id := sc.Opportunities()[0].ID
	preview, err := sc.BuildPlan(ctx, id, 2, 90)
	if err != nil || !preview.Executable {
		t.Fatalf("preview: %+v, %v", preview, err)
	}
	long.mu.Lock()
	aged := *long.data.Health
	aged.Book.SourceTime = now.Add(-6 * time.Second)
	long.data.Health = &aged
	long.mu.Unlock()
	s := &Server{ctx: ctx, logger: logger, scanner: sc,
		live: NewLiveDeps(ctx, logger, domain.NewSigningRequestStore(), nil, nil, nil, nil, nil)}
	body, _ := json.Marshal(map[string]any{
		"opportunity_id": id, "leverage": 2, "requested_notional": 90,
		"accounts": map[string]string{"pacifica": "11111111111111111111111111111111", "hyperliquid": "0x1111111111111111111111111111111111111111"},
		"agents":   map[string]string{"pacifica": "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA", "hyperliquid": "0x2222222222222222222222222222222222222222"},
	})
	for _, stream := range []bool{false, true} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/v1/live/prepare", bytes.NewReader(body))
		if stream {
			r.Header.Set("Accept", "application/x-ndjson")
		}
		s.handleLivePrepare(w, r)
		if !strings.Contains(w.Body.String(), "PLAN_NOT_EXECUTABLE") || !strings.Contains(w.Body.String(), "fresh BBO depth unavailable") {
			t.Fatalf("rejection reason missing: %d %s", w.Code, w.Body.String())
		}
		if !stream && w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("legacy status=%d", w.Code)
		}
		if stream && !strings.Contains(w.Body.String(), `"status":422`) {
			t.Fatal("stream lost rejection status")
		}
	}
}
