package api

import (
	"context"
	"hash/fnv"
	"log/slog"
	"strconv"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/scanner"
)

type opportunityResponse struct {
	domain.Opportunity
	Signal7d *scanner.OpportunitySignal `json:"signal_7d"`
}

type opportunitySignalSnapshot struct {
	sourceVersion      int64
	opportunityVersion uint64
	fundingRows        []scanner.SignalFundingRow
	signals            map[string]scanner.OpportunitySignal
}

func (s *Server) opportunitiesWithSignals(_ context.Context, opportunities []domain.Opportunity) ([]opportunityResponse, error) {
	if s.db == nil || len(opportunities) == 0 {
		return opportunityResponses(opportunities, nil), nil
	}

	version := opportunitySignalVersion(opportunities)
	s.signalMu.Lock()
	signals := s.signalSnapshot.signals
	now := time.Now()
	opportunityChanged := s.signalSnapshot.opportunityVersion != version
	checkSource := s.signalSnapshot.fundingRows == nil || !now.Before(s.signalNextSourceCheck)
	needsRefresh := opportunityChanged || checkSource
	cacheResult := "hit"
	if signals == nil {
		cacheResult = "miss"
	} else if needsRefresh {
		cacheResult = "stale"
	}
	if needsRefresh && !s.signalRefreshRunning {
		s.signalRefreshRunning = true
		refreshOpportunities := append([]domain.Opportunity(nil), opportunities...)
		go s.refreshOpportunitySignals(refreshOpportunities, version, checkSource)
	}
	s.signalMu.Unlock()
	s.logger.Info("opportunity signals cache", "result", cacheResult)

	return opportunityResponses(opportunities, signals), nil
}

func opportunitySignalVersion(opportunities []domain.Opportunity) uint64 {
	h := fnv.New64a()
	for _, opportunity := range opportunities {
		_, _ = h.Write([]byte(opportunity.ID))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(strconv.FormatFloat(opportunity.AnnualizedGrossEdge, 'g', -1, 64)))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(opportunity.Direction))
		_, _ = h.Write([]byte{0})
	}
	return h.Sum64()
}

func (s *Server) refreshOpportunitySignals(opportunities []domain.Opportunity, opportunityVersion uint64, checkSource bool) {
	started := time.Now()
	ctx := s.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	s.signalMu.Lock()
	sourceVersion := s.signalSnapshot.sourceVersion
	cachedRows := s.signalSnapshot.fundingRows
	s.signalMu.Unlock()

	if checkSource {
		if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(bucket_unix), 0) FROM market_snapshots_1h`).Scan(&sourceVersion); err != nil {
			s.signalMu.Lock()
			s.signalNextSourceCheck = time.Now().Add(time.Minute)
			s.signalMu.Unlock()
			s.finishSignalRefresh(err, started, sourceVersion, 0, 0)
			return
		}
		nextCheck := nextOpportunitySignalSourceCheck(time.Now().UTC(), sourceVersion)
		s.signalMu.Lock()
		s.signalNextSourceCheck = nextCheck
		s.signalMu.Unlock()
	}

	s.signalMu.Lock()
	unchanged := s.signalSnapshot.sourceVersion == sourceVersion && s.signalSnapshot.opportunityVersion == opportunityVersion
	cachedSourceVersion := s.signalSnapshot.sourceVersion
	s.signalMu.Unlock()
	if unchanged {
		s.finishSignalRefresh(nil, started, sourceVersion, 0, len(opportunities))
		return
	}
	if cachedSourceVersion == sourceVersion && cachedRows != nil {
		enriched := s.publishOpportunitySignals(sourceVersion, opportunityVersion, opportunities, cachedRows)
		s.finishSignalRefresh(nil, started, sourceVersion, 0, enriched)
		return
	}

	dbRows, err := s.db.QueryContext(ctx, `
		SELECT venue, asset, bucket_unix, funding_avg
		FROM market_snapshots_1h
		WHERE bucket_unix >= ? AND bucket_unix <= ?
		ORDER BY asset, venue, bucket_unix`, sourceVersion-int64((7*24*time.Hour)/time.Second), sourceVersion)
	if err != nil {
		s.finishSignalRefresh(err, started, sourceVersion, 0, 0)
		return
	}

	fundingRows := make([]scanner.SignalFundingRow, 0)
	for dbRows.Next() {
		var row scanner.SignalFundingRow
		if err := dbRows.Scan(&row.Venue, &row.Asset, &row.BucketUnix, &row.FundingRate); err != nil {
			_ = dbRows.Close()
			s.finishSignalRefresh(err, started, sourceVersion, len(fundingRows), 0)
			return
		}
		fundingRows = append(fundingRows, row)
	}
	if err := dbRows.Err(); err != nil {
		_ = dbRows.Close()
		s.finishSignalRefresh(err, started, sourceVersion, len(fundingRows), 0)
		return
	}
	_ = dbRows.Close()

	enriched := s.publishOpportunitySignals(sourceVersion, opportunityVersion, opportunities, fundingRows)
	s.finishSignalRefresh(nil, started, sourceVersion, len(fundingRows), enriched)
}

func nextOpportunitySignalSourceCheck(now time.Time, sourceVersion int64) time.Time {
	expectedClosedBucket := now.Truncate(time.Hour).Add(-time.Hour).Unix()
	if sourceVersion < expectedClosedBucket {
		return now.Add(time.Minute)
	}
	return now.Truncate(time.Hour).Add(time.Hour + time.Minute)
}

func (s *Server) publishOpportunitySignals(sourceVersion int64, opportunityVersion uint64, opportunities []domain.Opportunity, fundingRows []scanner.SignalFundingRow) int {
	signals := scanner.CalculateOpportunitySignals(opportunities, fundingRows)
	s.signalMu.Lock()
	s.signalSnapshot = opportunitySignalSnapshot{
		sourceVersion:      sourceVersion,
		opportunityVersion: opportunityVersion,
		fundingRows:        fundingRows,
		signals:            signals,
	}
	s.signalMu.Unlock()
	return len(signals)
}

func (s *Server) finishSignalRefresh(err error, started time.Time, sourceVersion int64, rows, enriched int) {
	s.signalMu.Lock()
	s.signalRefreshRunning = false
	s.signalMu.Unlock()

	args := []any{
		"source_version", strconv.FormatInt(sourceVersion, 10),
		"rows", rows,
		"opportunities_enriched", enriched,
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
	if err != nil {
		s.logger.Error("opportunity signals: rebuild failed", append(args, "err", err)...)
		return
	}
	s.logger.Log(context.Background(), slog.LevelInfo, "opportunity signals: rebuild complete", args...)
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
