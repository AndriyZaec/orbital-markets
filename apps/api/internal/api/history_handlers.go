package api

import (
	"context"
	"math"
	"net/http"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/db/sqlc"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
)

type historyPoint struct {
	T     string  `json:"t"`
	Basis float64 `json:"basis"`
	Edge  float64 `json:"edge"`
}

type historyResponse struct {
	Asset  string         `json:"asset"`
	VenueA string         `json:"venue_a"`
	VenueB string         `json:"venue_b"`
	Points []historyPoint `json:"points"`
}

// historyRow is the common shape paired across sources (raw, 5m, 1h).
type historyRow struct {
	TsUnix      int64
	MarkPrice   float64
	FundingRate float64
}

type historySource int

const (
	sourceRaw historySource = iota
	source5m
	source1h
)

// rangeSpec maps a range token to its window duration and source table.
//
// Source selection mirrors the retention windows: raw holds 7d, 5m holds 30d,
// 1h holds 1y. Anything longer than the source can supply would return empty.
var rangeSpec = map[string]struct {
	dur    time.Duration
	source historySource
}{
	"1h":  {1 * time.Hour, sourceRaw},
	"24h": {24 * time.Hour, sourceRaw},
	"7d":  {7 * 24 * time.Hour, sourceRaw},
	"30d": {30 * 24 * time.Hour, source5m},
	"90d": {90 * 24 * time.Hour, source1h},
	"1y":  {365 * 24 * time.Hour, source1h},
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	asset := r.URL.Query().Get("asset")
	venueA := r.URL.Query().Get("venue_a")
	venueB := r.URL.Query().Get("venue_b")
	rangeStr := r.URL.Query().Get("range")

	if asset == "" || venueA == "" || venueB == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "asset, venue_a, venue_b required"})
		return
	}

	spec, ok := rangeSpec[rangeStr]
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "range must be one of: 1h, 24h, 7d, 30d, 90d, 1y"})
		return
	}

	now := time.Now().UTC()
	start := now.Add(-spec.dur)
	queries := sqlc.New(s.db)

	rowsA, err := fetchHistoryRows(r.Context(), queries, spec.source, venueA, asset, start.Unix(), now.Unix())
	if err != nil {
		s.logger.Error("history: fetch venue_a", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to fetch data"})
		return
	}

	rowsB, err := fetchHistoryRows(r.Context(), queries, spec.source, venueB, asset, start.Unix(), now.Unix())
	if err != nil {
		s.logger.Error("history: fetch venue_b", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to fetch data"})
		return
	}

	points := pairHistoryRows(rowsA, rowsB, spec.source)

	if len(points) > 200 {
		step := len(points) / 200
		var ds []historyPoint
		for i := 0; i < len(points); i += step {
			ds = append(ds, points[i])
		}
		points = ds
	}

	writeJSON(w, http.StatusOK, historyResponse{
		Asset:  asset,
		VenueA: venueA,
		VenueB: venueB,
		Points: points,
	})
}

func fetchHistoryRows(ctx context.Context, q *sqlc.Queries, src historySource, venue, asset string, startUnix, endUnix int64) ([]historyRow, error) {
	switch src {
	case source5m:
		rows, err := q.List5mByVenueAsset(ctx, sqlc.List5mByVenueAssetParams{
			Venue:        venue,
			Asset:        asset,
			BucketUnix:   startUnix,
			BucketUnix_2: endUnix,
		})
		if err != nil {
			return nil, err
		}
		out := make([]historyRow, len(rows))
		for i, r := range rows {
			out[i] = historyRow{TsUnix: r.BucketUnix, MarkPrice: r.Close, FundingRate: r.FundingAvg}
		}
		return out, nil
	case source1h:
		rows, err := q.List1hByVenueAsset(ctx, sqlc.List1hByVenueAssetParams{
			Venue:        venue,
			Asset:        asset,
			BucketUnix:   startUnix,
			BucketUnix_2: endUnix,
		})
		if err != nil {
			return nil, err
		}
		out := make([]historyRow, len(rows))
		for i, r := range rows {
			out[i] = historyRow{TsUnix: r.BucketUnix, MarkPrice: r.Close, FundingRate: r.FundingAvg}
		}
		return out, nil
	default:
		rows, err := q.ListSnapshotsByVenueAsset(ctx, sqlc.ListSnapshotsByVenueAssetParams{
			Venue:    venue,
			Asset:    asset,
			TsUnix:   startUnix,
			TsUnix_2: endUnix,
		})
		if err != nil {
			return nil, err
		}
		out := make([]historyRow, len(rows))
		for i, r := range rows {
			out[i] = historyRow{TsUnix: r.TsUnix, MarkPrice: r.MarkPrice, FundingRate: r.FundingRate}
		}
		return out, nil
	}
}

func pairHistoryRows(a, b []historyRow, src historySource) []historyPoint {
	if len(a) == 0 || len(b) == 0 {
		return nil
	}

	// Tolerance scales with source granularity so paired points actually land in
	// the same bucket: 2m for 1-min raw, ~½ bucket for aggregated sources.
	tolerance := 2 * time.Minute
	switch src {
	case source5m:
		tolerance = 3 * time.Minute
	case source1h:
		tolerance = 30 * time.Minute
	}

	var points []historyPoint
	j := 0

	for _, sa := range a {
		ta := time.Unix(sa.TsUnix, 0).UTC()

		for j+1 < len(b) {
			tb1 := time.Unix(b[j].TsUnix, 0)
			tb2 := time.Unix(b[j+1].TsUnix, 0)
			if math.Abs(float64(tb2.Sub(ta))) < math.Abs(float64(tb1.Sub(ta))) {
				j++
			} else {
				break
			}
		}

		tb := time.Unix(b[j].TsUnix, 0)
		if math.Abs(float64(ta.Sub(tb))) > float64(tolerance) {
			continue
		}

		sb := b[j]
		basis := 0.0
		if sa.MarkPrice != 0 {
			basis = (sa.MarkPrice - sb.MarkPrice) / sa.MarkPrice
		}
		edge := domain.AnnualizedGrossEdge(sa.FundingRate, sb.FundingRate)

		points = append(points, historyPoint{
			T:     ta.Format(time.RFC3339),
			Basis: basis,
			Edge:  edge,
		})
	}

	return points
}
