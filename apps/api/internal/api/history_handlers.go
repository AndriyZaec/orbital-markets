package api

import (
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

var rangeMap = map[string]time.Duration{
	"1h":  1 * time.Hour,
	"24h": 24 * time.Hour,
	"7d":  7 * 24 * time.Hour,
	"30d": 30 * 24 * time.Hour,
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

	dur, ok := rangeMap[rangeStr]
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "range must be one of: 1h, 24h, 7d, 30d"})
		return
	}

	now := time.Now().UTC()
	start := now.Add(-dur)
	queries := sqlc.New(s.db)

	snapsA, err := queries.ListSnapshotsByVenueAsset(r.Context(), sqlc.ListSnapshotsByVenueAssetParams{
		Venue:    venueA,
		Asset:    asset,
		TsUnix:   start.Unix(),
		TsUnix_2: now.Unix(),
	})
	if err != nil {
		s.logger.Error("history: fetch venue_a", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to fetch data"})
		return
	}

	snapsB, err := queries.ListSnapshotsByVenueAsset(r.Context(), sqlc.ListSnapshotsByVenueAssetParams{
		Venue:    venueB,
		Asset:    asset,
		TsUnix:   start.Unix(),
		TsUnix_2: now.Unix(),
	})
	if err != nil {
		s.logger.Error("history: fetch venue_b", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to fetch data"})
		return
	}

	points := pairSnapshots(snapsA, snapsB)

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

func pairSnapshots(a, b []sqlc.MarketSnapshot) []historyPoint {
	if len(a) == 0 || len(b) == 0 {
		return nil
	}

	const tolerance = 2 * time.Minute
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
