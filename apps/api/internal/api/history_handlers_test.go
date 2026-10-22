package api

import (
	"encoding/json"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	appdb "github.com/AndriyZaec/orbital-markets/apps/api/internal/db"
)

func TestChartRangesUseBoundedHistorySources(t *testing.T) {
	tests := map[string]historySource{
		"1h":  sourceRaw,
		"24h": source5m,
		"7d":  source1h,
		"30d": source1h,
	}
	for rangeName, want := range tests {
		if got := rangeSpec[rangeName].source; got != want {
			t.Errorf("range %s source = %v, want %v", rangeName, got, want)
		}
	}
}

func TestPairHistoryRowsIncludesVenueFunding(t *testing.T) {
	ts := time.Now().UTC().Truncate(time.Minute).Unix()
	points := pairHistoryRows(
		[]historyRow{{TsUnix: ts, MarkPrice: 100, FundingRate: 0.0002}},
		[]historyRow{{TsUnix: ts, MarkPrice: 101, FundingRate: -0.0001}},
		sourceRaw,
	)
	if len(points) != 1 {
		t.Fatalf("points = %d, want 1", len(points))
	}
	point := points[0]
	if point.FundingA != 0.0002 || point.FundingB != -0.0001 {
		t.Fatalf("funding = %v/%v, want 0.0002/-0.0001", point.FundingA, point.FundingB)
	}
	wantEdge := 0.0003 * 8760
	if math.Abs(point.Edge-wantEdge) > 1e-9 {
		t.Fatalf("edge = %v, want %v", point.Edge, wantEdge)
	}
}

func TestHistoryPriceSpreadDoesNotFabricateMissingPrices(t *testing.T) {
	for _, price := range []float64{0, -1, math.NaN(), math.Inf(1)} {
		points := pairPriceHistoryRows([]historyRow{{TsUnix: 100, MarkPrice: price}}, []historyRow{{TsUnix: 100, MarkPrice: 100}})
		if len(points) != 1 || points[0].PriceSpread != nil {
			t.Fatalf("invalid price %v must not produce a spread: %+v", price, points)
		}
	}
}

func TestPriceHistoryPairsSourceTimesBeforeSampling(t *testing.T) {
	a := []historyRow{{TsUnix: 100, MarkPrice: 100}, {TsUnix: 160, MarkPrice: 101}}
	b := []historyRow{{TsUnix: 102, MarkPrice: 100}}
	points := pairPriceHistoryRows(a, b)
	if len(points) != 1 || points[0].PriceSpread == nil || *points[0].PriceSpread != 0 {
		t.Fatalf("feed loss fabricated divergence: %+v", points)
	}
	// A pair straddling entry must retain the earlier source time, not imply
	// that both observations happened after entry.
	points = pairPriceHistoryRows([]historyRow{{TsUnix: 104, MarkPrice: 100}}, []historyRow{{TsUnix: 100, MarkPrice: 101}})
	if len(points) != 1 || points[0].T != time.Unix(100, 0).UTC().Format(time.RFC3339) ||
		points[0].PriceSpread == nil || math.Abs(*points[0].PriceSpread-1/100.5) > 1e-12 {
		t.Fatalf("unexpected source time or spread: %+v", points)
	}
	if points[0].MarkPriceA == nil || *points[0].MarkPriceA != 100 || points[0].MarkPriceB == nil || *points[0].MarkPriceB != 101 {
		t.Fatalf("paired prices missing or assigned to the wrong venue: %+v", points[0])
	}
}

func TestPriceHistoryIgnoresIndependentClosesAndSeparatesFundingCache(t *testing.T) {
	database, err := appdb.Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	bucket := time.Now().UTC().Truncate(time.Hour).Add(-time.Hour).Unix()
	for _, row := range []struct {
		venue string
		ts    int64
		price float64
	}{
		{"a", bucket + 60, 100}, {"b", bucket + 62, 100}, {"a", bucket + 3540, 101},
	} {
		_, err := database.Exec(`INSERT INTO market_snapshots
			(venue,asset,market_key,mark_price,index_price,funding_rate,bid_price,ask_price,open_interest,ts_unix)
			VALUES (?, 'SOL', 'SOL', ?, ?, 0, ?, ?, 100, ?)`, row.venue, row.price, row.price, row.price, row.price, row.ts)
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, table := range []string{"market_snapshots_5m", "market_snapshots_1h"} {
		for _, row := range []struct {
			venue string
			price float64
		}{{"a", 101}, {"b", 100}} {
			_, err := database.Exec(`INSERT INTO `+table+`
				(venue,asset,bucket_unix,open,high,low,close,funding_avg,oi_avg,bid_avg,ask_avg)
				VALUES (?, 'SOL', ?, ?, ?, ?, ?, 0, 100, ?, ?)`, row.venue, bucket, row.price, row.price, row.price, row.price, row.price, row.price)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	s := &Server{analyticsDB: database, logger: slog.Default(), historyCache: newHistoryCache(8)}
	for _, window := range []string{"24h", "7d"} {
		path := "/api/v1/history?asset=SOL&venue_a=a&venue_b=b&range=" + window
		for _, series := range []string{"", "&series=price_spread"} {
			if window != "24h" && series != "" {
				continue
			}
			w := httptest.NewRecorder()
			s.handleHistory(w, httptest.NewRequest(http.MethodGet, path+series, nil))
			var response historyResponse
			if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &response) != nil {
				t.Fatalf("history failed: %d %s", w.Code, w.Body.String())
			}
			if len(response.Points) != 1 || response.Start == "" || response.End == "" {
				t.Fatalf("unexpected response: %+v", response)
			}
			point := response.Points[0]
			if series == "" && point.PriceSpread != nil {
				t.Fatal("aggregated funding closes must not expose a price spread")
			}
			if series != "" && (point.PriceSpread == nil || *point.PriceSpread != 0 || point.T != time.Unix(bucket+60, 0).UTC().Format(time.RFC3339)) {
				t.Fatalf("price history used mismatched closes or funding cache: %+v", point)
			}
		}
	}
	for _, window := range []string{"1h", "7d", "30d", "90d", "1y"} {
		w := httptest.NewRecorder()
		s.handleHistory(w, httptest.NewRequest(http.MethodGet, "/api/v1/history?asset=SOL&venue_a=a&venue_b=b&range="+window+"&series=price_spread", nil))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("price history must reject range %s before loading data", window)
		}
	}
}

func TestDownsampleHistoryPointsCapsResponse(t *testing.T) {
	points := make([]historyPoint, 720)
	for i := range points {
		points[i].T = time.Unix(int64(i), 0).UTC().Format(time.RFC3339)
	}
	got := downsampleHistoryPoints(points, 200)
	if len(got) != 200 {
		t.Fatalf("downsampled points = %d, want 200", len(got))
	}
	if got[0].T != points[0].T || got[len(got)-1].T != points[len(points)-1].T {
		t.Fatal("downsampled points must preserve the first and latest samples")
	}
}

func TestPriceHistoryPreservesGapSegmentsThroughDownsampling(t *testing.T) {
	for _, kind := range []string{"unpaired", "invalid", "both_missing", "healthy"} {
		t.Run(kind, func(t *testing.T) {
			var a, b []historyRow
			for i := 0; i < 1440; i++ {
				row := historyRow{TsUnix: int64(i * 60), MarkPrice: 100}
				if i == 720 && kind == "both_missing" {
					continue
				}
				if i == 720 && kind == "invalid" {
					row.MarkPrice = 0
				}
				a = append(a, row)
				if i == 720 && kind == "unpaired" {
					continue
				}
				b = append(b, historyRow{TsUnix: int64(i * 60), MarkPrice: 101})
			}
			points := downsampleHistoryPoints(pairPriceHistoryRows(a, b), 200)
			encoded, err := json.Marshal(points)
			if err != nil {
				t.Fatal(err)
			}
			var rows []map[string]any
			if err = json.Unmarshal(encoded, &rows); err != nil {
				t.Fatal(err)
			}
			ids := map[any]bool{}
			for _, row := range rows {
				ids[row["segment"]] = true
			}
			want := 2
			if kind == "healthy" {
				want = 1
			}
			if len(ids) != want {
				t.Fatalf("%s: output lost gap boundaries: segments=%d want=%d, points=%d", kind, len(ids), want, len(rows))
			}
			if len(points) > 200 {
				t.Fatal("gap preservation exceeded the response cap")
			}
		})
	}
}
