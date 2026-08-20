package api

import (
	"context"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
)

const (
	signalAPRThreshold       = 0.01
	signalMinimumSamples     = 24
	signalActiveShare        = 0.20
	signalPersistentShare    = 0.60
	signalDirectionThreshold = 0.70
)

type opportunitySignal struct {
	Status               string  `json:"status"`
	Activity             float64 `json:"activity"`
	DirectionConsistency float64 `json:"direction_consistency"`
	AverageEdge          float64 `json:"average_edge"`
	Samples              int     `json:"samples"`
}

type opportunityResponse struct {
	domain.Opportunity
	Signal7d *opportunitySignal `json:"signal_7d"`
}

type signalFundingRow struct {
	Venue       string
	Asset       string
	BucketUnix  int64
	FundingRate float64
}

type signalStats struct {
	Samples   int
	Active    int
	LongA     int
	LongB     int
	TotalEdge float64
}

type signalSeriesKey struct {
	Asset string
	Venue string
}

func (s *Server) opportunitiesWithSignals(ctx context.Context, opportunities []domain.Opportunity) ([]opportunityResponse, error) {
	if s.db == nil || len(opportunities) == 0 {
		return opportunityResponses(opportunities, nil), nil
	}

	s.signalMu.Lock()
	defer s.signalMu.Unlock()
	if time.Since(s.signalCachedAt) < time.Minute {
		return opportunityResponses(opportunities, s.signalCache), nil
	}

	now := time.Now().UTC()
	rows, err := s.db.QueryContext(ctx, `
		SELECT venue, asset, bucket_unix, funding_avg
		FROM market_snapshots_1h
		WHERE bucket_unix >= ? AND bucket_unix <= ?
		ORDER BY asset, venue, bucket_unix`, now.Add(-7*24*time.Hour).Unix(), now.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	fundingRows := make([]signalFundingRow, 0)
	for rows.Next() {
		var row signalFundingRow
		if err := rows.Scan(&row.Venue, &row.Asset, &row.BucketUnix, &row.FundingRate); err != nil {
			return nil, err
		}
		fundingRows = append(fundingRows, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	signals := calculateOpportunitySignals(opportunities, fundingRows)
	s.signalCache = signals
	s.signalCachedAt = now
	return opportunityResponses(opportunities, signals), nil
}

func opportunityResponses(opportunities []domain.Opportunity, signals map[string]opportunitySignal) []opportunityResponse {
	responses := make([]opportunityResponse, len(opportunities))
	for i, opportunity := range opportunities {
		responses[i].Opportunity = opportunity
		if signal, ok := signals[opportunity.ID]; ok {
			responses[i].Signal7d = &signal
		}
	}
	return responses
}

func calculateOpportunitySignals(opportunities []domain.Opportunity, rows []signalFundingRow) map[string]opportunitySignal {
	series := make(map[signalSeriesKey]map[int64]float64)
	for _, row := range rows {
		key := signalSeriesKey{Asset: row.Asset, Venue: row.Venue}
		if series[key] == nil {
			series[key] = make(map[int64]float64)
		}
		series[key][row.BucketUnix] = row.FundingRate
	}

	signals := make(map[string]opportunitySignal)
	for _, opportunity := range opportunities {
		ratesA := series[signalSeriesKey{Asset: opportunity.Asset, Venue: opportunity.VenuePair.VenueA}]
		ratesB := series[signalSeriesKey{Asset: opportunity.Asset, Venue: opportunity.VenuePair.VenueB}]
		stats := signalStats{}
		for bucket, rateA := range ratesA {
			rateB, ok := ratesB[bucket]
			if !ok {
				continue
			}
			spread := rateA - rateB
			edge := spread
			if edge < 0 {
				edge = -edge
			}
			stats.Samples++
			stats.TotalEdge += edge
			if domain.AnnualizeRate(edge) < signalAPRThreshold {
				continue
			}
			stats.Active++
			if spread > 0 {
				stats.LongB++
			} else {
				stats.LongA++
			}
		}
		if stats.Samples > 0 {
			signals[opportunity.ID] = classifyOpportunitySignal(opportunity, stats)
		}
	}
	return signals
}

func classifyOpportunitySignal(opportunity domain.Opportunity, stats signalStats) opportunitySignal {
	activity := float64(stats.Active) / float64(stats.Samples)
	consistency := 0.0
	dominantDirection := domain.Direction("")
	if stats.Active > 0 {
		dominant := stats.LongA
		dominantDirection = domain.DirectionLongA
		if stats.LongB > dominant {
			dominant = stats.LongB
			dominantDirection = domain.DirectionLongB
		}
		consistency = float64(dominant) / float64(stats.Active)
	}

	status := "flat"
	currentActive := opportunity.AnnualizedGrossEdge >= signalAPRThreshold
	switch {
	case stats.Samples < signalMinimumSamples:
		status = "limited"
	case !currentActive && activity >= signalActiveShare:
		status = "faded"
	case !currentActive:
		status = "flat"
	case activity < signalActiveShare:
		status = "new"
	case consistency < signalDirectionThreshold:
		status = "choppy"
	case dominantDirection != opportunity.Direction:
		status = "reversed"
	case activity >= signalPersistentShare:
		status = "persistent"
	default:
		status = "intermittent"
	}

	return opportunitySignal{
		Status:               status,
		Activity:             activity,
		DirectionConsistency: consistency,
		AverageEdge:          domain.AnnualizeRate(stats.TotalEdge / float64(stats.Samples)),
		Samples:              stats.Samples,
	}
}
