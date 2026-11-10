package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

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

func TestPrepareWaitsForBookAgingAfterExecutablePreview(t *testing.T) {
	f := newLocalPrepareFixture(t)
	f.s.scanner.EnableExecutionLiquidity(nil)
	preview, err := f.s.scanner.BuildPlan(context.Background(), f.body["opportunity_id"].(string), 2, 90)
	if err != nil || !preview.Executable {
		t.Fatalf("preview: %+v, %v", preview, err)
	}
	agePrepareBook(f)
	for _, stream := range []bool{false, true} {
		requestCtx, cancelRequest := context.WithTimeout(context.Background(), 30*time.Millisecond)
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/v1/live/prepare", jsonBody(t, f.body)).WithContext(requestCtx)
		if stream {
			r.Header.Set("Accept", "application/x-ndjson")
		}
		f.s.handleLivePrepare(w, r)
		cancelRequest()
		if !strings.Contains(w.Body.String(), "PREPARE_TIMEOUT") {
			t.Fatalf("missing bounded readiness timeout: %d %s", w.Code, w.Body.String())
		}
		if !stream && w.Code != http.StatusGatewayTimeout {
			t.Fatalf("legacy status=%d", w.Code)
		}
		if stream && (!strings.Contains(w.Body.String(), `"status":504`) || !strings.Contains(w.Body.String(), `"stage":"waiting_bbo"`)) {
			t.Fatal("stream lost waiting progress or timeout status")
		}
	}
}
