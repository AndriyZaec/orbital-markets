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
	T        string  `json:"t"`
	Basis    float64 `json:"basis"`
	Edge     float64 `json:"edge"`
	FundingA float64 `json:"funding_a"`
	FundingB float64 `json:"funding_b"`
}

type historyResponse struct {
	Asset  string         `json:"asset"`
	VenueA string         `json:"venue_a"`
	VenueB string         `json:"venue_b"`
	Points []historyPoint `json:"points"`
}

type historyLoadParams struct {
	asset    string
	venueA   string
	venueB   string
	rangeStr string
	spec     struct {
		dur    time.Duration
		source historySource
	}
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
	"24h": {24 * time.Hour, source5m},
	"7d":  {7 * 24 * time.Hour, source1h},
	"30d": {30 * 24 * time.Hour, source1h},
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

	params := historyLoadParams{asset: asset, venueA: venueA, venueB: venueB, rangeStr: rangeStr, spec: spec}
	key := historyCacheKey{asset: asset, venueA: venueA, venueB: venueB, range_: rangeStr}
	refreshCtx := s.ctx
	if refreshCtx == nil {
		refreshCtx = context.Background()
	}
	response, cacheResult, err := s.historyCache.get(
		r.Context(),
		refreshCtx,
		key,
		historyCacheTTL(spec.source),
		func(ctx context.Context) (historyResponse, error) {
			started := time.Now()
			ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			response, err := s.loadHistory(ctx, params)
			if err != nil {
				args := []any{
					"err", err,
					"asset", asset,
					"venue_a", venueA,
					"venue_b", venueB,
					"range", rangeStr,
					"duration_ms", time.Since(started).Milliseconds(),
				}
				if s.db != nil {
					stats := s.db.Stats()
					args = append(args,
						"db_open", stats.OpenConnections,
						"db_in_use", stats.InUse,
						"db_wait_count", stats.WaitCount,
						"db_wait_ms", stats.WaitDuration.Milliseconds(),
					)
				}
				s.logger.Error("history cache: load failed", args...)
			}
			return response, err
		},
	)
	s.logger.Info("history cache", "result", cacheResult, "asset", asset, "venue_a", venueA, "venue_b", venueB, "range", rangeStr)
	if err != nil {
		s.logger.Error("history: load", "err", err, "cache_result", cacheResult)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to fetch data"})
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func historyCacheTTL(source historySource) time.Duration {
	switch source {
	case source5m:
		return 5 * time.Minute
	case source1h:
		return time.Hour
	default:
		return time.Minute
	}
}

func (s *Server) loadHistory(ctx context.Context, params historyLoadParams) (historyResponse, error) {
	requestStarted := time.Now()
	now := time.Now().UTC()
	start := now.Add(-params.spec.dur)
	queries := sqlc.New(s.db)

	fetchAStarted := time.Now()
	rowsA, err := fetchHistoryRows(ctx, queries, params.spec.source, params.venueA, params.asset, start.Unix(), now.Unix())
	fetchADuration := time.Since(fetchAStarted)
	if err != nil {
		return historyResponse{}, err
	}

	fetchBStarted := time.Now()
	rowsB, err := fetchHistoryRows(ctx, queries, params.spec.source, params.venueB, params.asset, start.Unix(), now.Unix())
	fetchBDuration := time.Since(fetchBStarted)
	if err != nil {
		return historyResponse{}, err
	}

	pairStarted := time.Now()
	points := downsampleHistoryPoints(pairHistoryRows(rowsA, rowsB, params.spec.source), 200)
	pairDuration := time.Since(pairStarted)
	totalDuration := time.Since(requestStarted)
	logArgs := []any{
		"asset", params.asset,
		"venue_a", params.venueA,
		"venue_b", params.venueB,
		"range", params.rangeStr,
		"source", params.spec.source,
		"rows_a", len(rowsA),
		"rows_b", len(rowsB),
		"points", len(points),
		"fetch_a_ms", fetchADuration.Milliseconds(),
		"fetch_b_ms", fetchBDuration.Milliseconds(),
		"pair_ms", pairDuration.Milliseconds(),
		"total_ms", totalDuration.Milliseconds(),
	}
	if s.db != nil {
		stats := s.db.Stats()
		logArgs = append(logArgs,
			"db_open", stats.OpenConnections,
			"db_in_use", stats.InUse,
			"db_wait_count", stats.WaitCount,
			"db_wait_ms", stats.WaitDuration.Milliseconds(),
		)
	}
	if totalDuration >= 100*time.Millisecond {
		s.logger.Warn("history: slow load", logArgs...)
	} else {
		s.logger.Info("history: cache rebuild complete", logArgs...)
	}

	return historyResponse{Asset: params.asset, VenueA: params.venueA, VenueB: params.venueB, Points: points}, nil
}

func downsampleHistoryPoints(points []historyPoint, maxPoints int) []historyPoint {
	if maxPoints <= 0 || len(points) <= maxPoints {
		return points
	}
	downsampled := make([]historyPoint, maxPoints)
	for i := range maxPoints {
		index := i * (len(points) - 1) / (maxPoints - 1)
		downsampled[i] = points[index]
	}
	return downsampled
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
			T:        ta.Format(time.RFC3339),
			Basis:    basis,
			Edge:     edge,
			FundingA: sa.FundingRate,
			FundingB: sb.FundingRate,
		})
	}

	return points
}
