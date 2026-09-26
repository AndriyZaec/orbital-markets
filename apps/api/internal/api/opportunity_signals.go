package api

import (
	"context"
	"hash/fnv"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/scanner"
)

const opportunitySignalRetryDelay = time.Minute

type opportunityResponse struct {
	domain.Opportunity
	Signal7d        *scanner.OpportunitySignal `json:"signal_7d"`
	Signal7dState   string                     `json:"signal_7d_state"`
	Signal7dVersion uint64                     `json:"signal_7d_version"`
}

type opportunitySignalSource struct {
	rollupRevision uint64
	sourceBucket   int64
	fundingRows    []scanner.SignalFundingRow
}

type opportunitySignalPublication struct {
	opportunityVersion uint64
	signals            map[string]scanner.OpportunitySignal
}

type opportunitySignalBuild struct {
	source        opportunitySignalSource
	publication   opportunitySignalPublication
	sourceChanged bool
	rowsRead      int
}

type opportunitySignalBuilder func(
	context.Context,
	[]domain.Opportunity,
	uint64,
	opportunitySignalSource,
	opportunitySignalPublication,
	bool,
) (opportunitySignalBuild, error)

type opportunitySignalProjection struct {
	mu sync.Mutex

	source                    opportunitySignalSource
	publication               opportunitySignalPublication
	desiredVersion            uint64
	publishedVersion          uint64
	workerRunning             bool
	sourceCheckQueued         bool
	sourceCheckFailed         bool
	lastBuildFailed           bool
	retryScheduled            bool
	retryAttempted            bool
	nextSourceCheck           time.Time
	desiredOpportunities      []domain.Opportunity
	knownOpportunities        map[string]domain.Opportunity
	desiredOpportunityVersion uint64

	build    opportunitySignalBuilder
	launch   func(func())
	schedule func(time.Duration, func())
}

func (s *Server) opportunitiesWithSignals(_ context.Context, opportunities []domain.Opportunity) ([]opportunityResponse, error) {
	if s.db == nil || len(opportunities) == 0 {
		return opportunityResponses(opportunities, nil, 0, 0, true, true), nil
	}

	projection := &s.signals
	projection.mu.Lock()
	changed := projection.updateOpportunities(opportunities)
	if changed {
		projection.desiredVersion++
	}
	now := time.Now()
	sourceCheckDue := !now.Before(projection.nextSourceCheck)
	if projection.source.fundingRows == nil || sourceCheckDue {
		projection.sourceCheckQueued = true
	}
	newCycle := changed || sourceCheckDue
	if newCycle {
		projection.retryAttempted = false
	}
	needsWorker := projection.desiredVersion > projection.publishedVersion || projection.sourceCheckQueued
	startWorker := needsWorker && !projection.workerRunning && !projection.retryAttempted &&
		(!projection.retryScheduled || newCycle)
	if startWorker {
		projection.workerRunning = true
	}
	signals := projection.publication.signals
	desiredVersion := projection.desiredVersion
	publishedVersion := projection.publishedVersion
	sourceCheckFailed := projection.sourceCheckFailed
	lastBuildFailed := projection.lastBuildFailed
	cacheResult := "hit"
	if signals == nil {
		cacheResult = "miss"
	} else if desiredVersion > publishedVersion || sourceCheckFailed {
		cacheResult = "stale"
	}
	projection.mu.Unlock()

	if startWorker {
		projection.start(func() { s.refreshOpportunitySignals() })
	}
	s.logger.Info("opportunity signals cache",
		"result", cacheResult,
		"desired_version", desiredVersion,
		"published_version", publishedVersion,
	)
	return opportunityResponses(
		opportunities, signals, desiredVersion, publishedVersion, sourceCheckFailed, lastBuildFailed,
	), nil
}

func (p *opportunitySignalProjection) updateOpportunities(opportunities []domain.Opportunity) bool {
	if p.knownOpportunities == nil {
		p.knownOpportunities = make(map[string]domain.Opportunity)
	}
	for _, opportunity := range opportunities {
		p.knownOpportunities[opportunity.ID] = opportunity
	}
	known := make([]domain.Opportunity, 0, len(p.knownOpportunities))
	for _, opportunity := range p.knownOpportunities {
		known = append(known, opportunity)
	}
	sort.Slice(known, func(i, j int) bool { return known[i].ID < known[j].ID })
	version := opportunitySignalVersion(known)
	if version == p.desiredOpportunityVersion {
		return false
	}
	p.desiredOpportunities = known
	p.desiredOpportunityVersion = version
	return true
}

func (p *opportunitySignalProjection) start(worker func()) {
	if p.launch != nil {
		p.launch(worker)
		return
	}
	go worker()
}

func (p *opportunitySignalProjection) retryAfter(delay time.Duration, retry func()) {
	if p.schedule != nil {
		p.schedule(delay, retry)
		return
	}
	time.AfterFunc(delay, retry)
}

func opportunitySignalVersion(opportunities []domain.Opportunity) uint64 {
	h := fnv.New64a()
	for _, opportunity := range opportunities {
		_, _ = h.Write([]byte(opportunity.ID))
		_, _ = h.Write([]byte{0})
		if scanner.OpportunitySignalActive(opportunity) {
			_, _ = h.Write([]byte{1})
		} else {
			_, _ = h.Write([]byte{0})
		}
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(opportunity.Direction))
		_, _ = h.Write([]byte{0})
	}
	return h.Sum64()
}

func (s *Server) refreshOpportunitySignals() {
	projection := &s.signals
	for {
		projection.mu.Lock()
		targetVersion := projection.desiredVersion
		if targetVersion <= projection.publishedVersion && !projection.sourceCheckQueued {
			projection.workerRunning = false
			projection.mu.Unlock()
			return
		}
		opportunities := append([]domain.Opportunity(nil), projection.desiredOpportunities...)
		opportunityVersion := opportunitySignalVersion(opportunities)
		checkSource := projection.sourceCheckQueued
		projection.sourceCheckQueued = false
		if checkSource {
			projection.nextSourceCheck = time.Now().Add(opportunitySignalRetryDelay)
		}
		cachedSource := projection.source
		published := projection.publication
		builder := projection.build
		projection.mu.Unlock()

		started := time.Now()
		ctx := s.ctx
		if ctx == nil {
			ctx = context.Background()
		}
		ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		if builder == nil {
			builder = s.buildOpportunitySignals
		}
		result, err := builder(ctx, opportunities, opportunityVersion, cachedSource, published, checkSource)
		cancel()

		projection.mu.Lock()
		if err != nil {
			projection.lastBuildFailed = true
			if checkSource {
				projection.sourceCheckFailed = true
			}
			followUp := projection.desiredVersion > targetVersion || projection.sourceCheckQueued
			scheduleRetry := !followUp && !projection.retryScheduled && !projection.retryAttempted &&
				(projection.desiredVersion > projection.publishedVersion || projection.sourceCheckFailed)
			if scheduleRetry {
				projection.retryScheduled = true
			}
			if !followUp {
				projection.workerRunning = false
			}
			desiredVersion := projection.desiredVersion
			publishedVersion := projection.publishedVersion
			projection.mu.Unlock()

			s.logOpportunitySignalBuild(err, started, cachedSource.rollupRevision, 0, len(published.signals), desiredVersion, publishedVersion, followUp)
			if scheduleRetry {
				projection.retryAfter(opportunitySignalRetryDelay, func() { s.retryOpportunitySignals() })
			}
			if !followUp {
				return
			}
			continue
		}

		if checkSource {
			projection.sourceCheckFailed = false
			projection.nextSourceCheck = nextOpportunitySignalSourceCheck(time.Now().UTC())
		}
		projection.source = result.source
		if result.sourceChanged {
			projection.desiredVersion++
		}
		publicationCurrent := opportunityVersion == projection.desiredOpportunityVersion &&
			result.publication.opportunityVersion == opportunityVersion
		if publicationCurrent {
			projection.publication = result.publication
			projection.publishedVersion = projection.desiredVersion
			projection.lastBuildFailed = false
			projection.retryAttempted = false
		}
		followUp := projection.desiredVersion > projection.publishedVersion || projection.sourceCheckQueued
		if !followUp {
			projection.workerRunning = false
		}
		desiredVersion := projection.desiredVersion
		publishedVersion := projection.publishedVersion
		projection.mu.Unlock()

		s.logOpportunitySignalBuild(nil, started, result.source.rollupRevision, result.rowsRead, len(result.publication.signals), desiredVersion, publishedVersion, followUp)
		if !followUp {
			return
		}
	}
}

func (s *Server) retryOpportunitySignals() {
	if s.ctx != nil {
		select {
		case <-s.ctx.Done():
			return
		default:
		}
	}
	projection := &s.signals
	projection.mu.Lock()
	projection.retryScheduled = false
	projection.retryAttempted = true
	if projection.sourceCheckFailed {
		projection.sourceCheckQueued = true
	}
	startWorker := (projection.desiredVersion > projection.publishedVersion || projection.sourceCheckQueued) && !projection.workerRunning
	if startWorker {
		projection.workerRunning = true
	}
	projection.mu.Unlock()
	if startWorker {
		projection.start(func() { s.refreshOpportunitySignals() })
	}
}

func (s *Server) buildOpportunitySignals(
	ctx context.Context,
	opportunities []domain.Opportunity,
	opportunityVersion uint64,
	cached opportunitySignalSource,
	published opportunitySignalPublication,
	checkSource bool,
) (opportunitySignalBuild, error) {
	source := cached
	if checkSource {
		var rollupRevision uint64
		var sourceBucket int64
		if err := s.db.QueryRowContext(ctx, `
			SELECT revision, COALESCE((SELECT MAX(bucket_unix) FROM market_snapshots_1h), 0)
			FROM rollup_revisions
			WHERE name = 'market_snapshots_1h'`).Scan(&rollupRevision, &sourceBucket); err != nil {
			return opportunitySignalBuild{}, err
		}
		source.rollupRevision = rollupRevision
		source.sourceBucket = sourceBucket
	}
	sourceChanged := source.rollupRevision != cached.rollupRevision || cached.fundingRows == nil
	rowsRead := 0
	if sourceChanged {
		rows, err := s.db.QueryContext(ctx, `
			SELECT venue, asset, bucket_unix, funding_avg
			FROM market_snapshots_1h
			WHERE bucket_unix >= ? AND bucket_unix <= ?
			ORDER BY asset, venue, bucket_unix`, source.sourceBucket-int64((7*24*time.Hour)/time.Second), source.sourceBucket)
		if err != nil {
			return opportunitySignalBuild{}, err
		}
		fundingRows := make([]scanner.SignalFundingRow, 0)
		for rows.Next() {
			var row scanner.SignalFundingRow
			if err := rows.Scan(&row.Venue, &row.Asset, &row.BucketUnix, &row.FundingRate); err != nil {
				_ = rows.Close()
				return opportunitySignalBuild{}, err
			}
			fundingRows = append(fundingRows, row)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return opportunitySignalBuild{}, err
		}
		if err := rows.Close(); err != nil {
			return opportunitySignalBuild{}, err
		}
		source.fundingRows = fundingRows
		rowsRead = len(fundingRows)
	}

	rebuild := sourceChanged || opportunityVersion != published.opportunityVersion
	result := opportunitySignalBuild{
		source: source, sourceChanged: sourceChanged, rowsRead: rowsRead,
		publication: published,
	}
	if rebuild {
		result.publication = opportunitySignalPublication{
			opportunityVersion: opportunityVersion,
			signals:            scanner.CalculateOpportunitySignals(opportunities, source.fundingRows),
		}
	}
	return result, nil
}

func nextOpportunitySignalSourceCheck(now time.Time) time.Time {
	return now.Add(time.Minute)
}

func (s *Server) logOpportunitySignalBuild(
	err error,
	started time.Time,
	rollupRevision uint64,
	rows int,
	enriched int,
	desiredVersion uint64,
	publishedVersion uint64,
	followUp bool,
) {
	args := []any{
		"rollup_revision", rollupRevision,
		"desired_version", desiredVersion,
		"published_version", publishedVersion,
		"queued_follow_up", followUp,
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

func opportunityResponses(
	opportunities []domain.Opportunity,
	signals map[string]scanner.OpportunitySignal,
	desiredVersion uint64,
	publishedVersion uint64,
	sourceCheckFailed bool,
	lastBuildFailed bool,
) []opportunityResponse {
	responses := make([]opportunityResponse, len(opportunities))
	for i, opportunity := range opportunities {
		responses[i].Opportunity = opportunity
		responses[i].Signal7dVersion = publishedVersion
		if signal, ok := signals[opportunity.ID]; ok {
			responses[i].Signal7d = &signal
			responses[i].Signal7dState = "ready"
			if sourceCheckFailed || lastBuildFailed {
				responses[i].Signal7dState = "stale"
			} else if publishedVersion < desiredVersion {
				responses[i].Signal7dState = "refreshing"
			}
			continue
		}
		responses[i].Signal7dState = "loading"
		if lastBuildFailed || sourceCheckFailed || publishedVersion == desiredVersion {
			responses[i].Signal7dState = "unavailable"
		}
	}
	return responses
}
