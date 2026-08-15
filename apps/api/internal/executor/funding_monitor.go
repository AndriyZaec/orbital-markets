package executor

import (
	"context"
	"log/slog"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
)

const (
	fundingMonitorInterval   = 10 * time.Second
	fundingSyncInterval      = time.Minute
	fundingFinalizationDelay = 30 * time.Second
)

type FundingMonitor struct {
	store                   *Store
	sources                 map[string]venue.FundingHistory
	attemptedAt             map[string]time.Time
	syncedAt                map[string]time.Time
	finalizationAttemptedAt map[string]time.Time
	logger                  *slog.Logger
}

func NewFundingMonitor(logger *slog.Logger, store *Store, sources map[string]venue.FundingHistory) *FundingMonitor {
	return &FundingMonitor{
		store: store, sources: sources,
		attemptedAt:             make(map[string]time.Time),
		syncedAt:                make(map[string]time.Time),
		finalizationAttemptedAt: make(map[string]time.Time),
		logger:                  logger,
	}
}

func (m *FundingMonitor) Run(ctx context.Context) {
	ticker := time.NewTicker(fundingMonitorInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.tick(ctx)
		}
	}
}

func (m *FundingMonitor) tick(ctx context.Context) {
	positions, err := m.store.ListOpenPositions(ctx)
	if err != nil {
		m.logger.Warn("funding monitor: list open positions", "err", err)
		return
	}
	for i := range positions {
		m.syncOpen(ctx, &positions[i])
	}
	m.finalizeClosed(ctx)
}

func (m *FundingMonitor) syncOpen(ctx context.Context, position *LivePosition) {
	startedAt, err := time.Parse(time.RFC3339, position.StartedAt)
	if err != nil {
		return
	}
	realized, ok := m.realized(ctx, position, startedAt, time.Now().UTC(), false)
	if !ok {
		return
	}
	if err := m.store.UpdateRealizedFunding(ctx, position.ID, realized); err != nil {
		m.logger.Warn("funding monitor: update realized funding", "err", err, "id", position.ID)
	}
}

func (m *FundingMonitor) realized(ctx context.Context, position *LivePosition, since, until time.Time, force bool) (float64, bool) {
	if len(m.sources) == 0 {
		return 0, false
	}
	now := time.Now().UTC()
	lastSync := m.syncedAt[position.ID]
	if force || lastSync.IsZero() || now.Sub(lastSync) >= fundingSyncInterval {
		lastAttempt := m.attemptedAt[position.ID]
		if !force && !lastAttempt.IsZero() && now.Sub(lastAttempt) < fundingSyncInterval {
			return 0, false
		}
		m.attemptedAt[position.ID] = now
		requests := []struct {
			venue   string
			account string
		}{
			{venue: "pacifica", account: position.AccountPacifica},
			{venue: "hyperliquid", account: position.AccountHyperliquid},
		}
		for _, request := range requests {
			source, ok := m.sources[request.venue]
			if !ok || request.account == "" {
				return 0, false
			}
			payments, err := source.FundingPayments(ctx, request.account, position.Asset, since, until)
			if err != nil {
				m.logger.Warn("funding monitor: fetch payments", "err", err, "id", position.ID, "venue", request.venue)
				return 0, false
			}
			if err := m.store.InsertFundingPayments(ctx, position.ID, payments); err != nil {
				m.logger.Warn("funding monitor: persist payments", "err", err, "id", position.ID, "venue", request.venue)
				return 0, false
			}
		}
		if err := m.store.RecordFundingSync(ctx, position.ID, false); err != nil {
			m.logger.Warn("funding monitor: record sync", "err", err, "id", position.ID)
			return 0, false
		}
		m.syncedAt[position.ID] = now
	}
	total, err := m.store.SumFundingPayments(ctx, position.ID)
	if err != nil {
		m.logger.Warn("funding monitor: sum payments", "err", err, "id", position.ID)
		return 0, false
	}
	return total, true
}

func (m *FundingMonitor) finalizeClosed(ctx context.Context) {
	positions, err := m.store.ListUnfinalizedClosedPositions(ctx)
	if err != nil {
		m.logger.Warn("funding monitor: list finalizations", "err", err)
		return
	}
	for i := range positions {
		position := &positions[i]
		completedAt, completedErr := time.Parse(time.RFC3339, position.CompletedAt)
		startedAt, startedErr := time.Parse(time.RFC3339, position.StartedAt)
		if completedErr != nil || startedErr != nil || time.Since(completedAt) < fundingFinalizationDelay {
			continue
		}
		lastAttempt := m.finalizationAttemptedAt[position.ID]
		if !lastAttempt.IsZero() && time.Since(lastAttempt) < fundingSyncInterval {
			continue
		}
		m.finalizationAttemptedAt[position.ID] = time.Now()
		realized, ok := m.realized(ctx, position, startedAt, completedAt, true)
		if !ok {
			continue
		}
		if err := m.store.UpdateRealizedFunding(ctx, position.ID, realized); err != nil {
			m.logger.Warn("funding monitor: finalize value", "err", err, "id", position.ID)
			continue
		}
		if err := m.store.RecordFundingSync(ctx, position.ID, true); err != nil {
			m.logger.Warn("funding monitor: mark finalized", "err", err, "id", position.ID)
		}
	}
}
