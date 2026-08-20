package api

import (
	"context"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/scanner"
)

type opportunityResponse struct {
	domain.Opportunity
	Signal7d *scanner.OpportunitySignal `json:"signal_7d"`
}

func (s *Server) opportunitiesWithSignals(ctx context.Context, opportunities []domain.Opportunity) ([]opportunityResponse, error) {
	if s.db == nil || len(opportunities) == 0 {
		return opportunityResponses(opportunities, nil), nil
	}

	rows, err := s.cachedSignalFundingRows(ctx)
	if err != nil {
		return nil, err
	}
	return opportunityResponses(opportunities, scanner.CalculateOpportunitySignals(opportunities, rows)), nil
}

func (s *Server) cachedSignalFundingRows(ctx context.Context) ([]scanner.SignalFundingRow, error) {
	s.signalMu.Lock()
	defer s.signalMu.Unlock()
	if time.Since(s.signalCachedAt) < time.Minute {
		return s.signalFundingRows, nil
	}

	now := time.Now().UTC()
	dbRows, err := s.db.QueryContext(ctx, `
		SELECT venue, asset, bucket_unix, funding_avg
		FROM market_snapshots_1h
		WHERE bucket_unix >= ? AND bucket_unix <= ?
		ORDER BY asset, venue, bucket_unix`, now.Add(-7*24*time.Hour).Unix(), now.Unix())
	if err != nil {
		return nil, err
	}
	defer dbRows.Close()

	fundingRows := make([]scanner.SignalFundingRow, 0)
	for dbRows.Next() {
		var row scanner.SignalFundingRow
		if err := dbRows.Scan(&row.Venue, &row.Asset, &row.BucketUnix, &row.FundingRate); err != nil {
			return nil, err
		}
		fundingRows = append(fundingRows, row)
	}
	if err := dbRows.Err(); err != nil {
		return nil, err
	}

	s.signalFundingRows = fundingRows
	s.signalCachedAt = now
	return fundingRows, nil
}

func opportunityResponses(opportunities []domain.Opportunity, signals map[string]scanner.OpportunitySignal) []opportunityResponse {
	responses := make([]opportunityResponse, len(opportunities))
	for i, opportunity := range opportunities {
		responses[i].Opportunity = opportunity
		if signal, ok := signals[opportunity.ID]; ok {
			responses[i].Signal7d = &signal
		}
	}
	return responses
}
