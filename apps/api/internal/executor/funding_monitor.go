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
	maxFundingWindowsPerSync = 4
)

type FundingMonitor struct {
	store                   *Store
	sources                 map[string]venue.FundingHistory
	attemptedAt             map[string]time.Time
	syncedAt                map[string]time.Time
	syncedTargets           map[string]time.Time
	finalizationAttemptedAt map[string]time.Time
	logger                  *slog.Logger
}

func NewFundingMonitor(logger *slog.Logger, store *Store, sources map[string]venue.FundingHistory) *FundingMonitor {
	return &FundingMonitor{
		store: store, sources: sources,
		attemptedAt:             make(map[string]time.Time),
		syncedAt:                make(map[string]time.Time),
		syncedTargets:           make(map[string]time.Time),
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
	openedAt, err := time.Parse(time.RFC3339, position.OpenedAt)
	if err != nil {
		return
	}
	m.realized(ctx, position, openedAt, time.Now().UTC(), false)
}

func (m *FundingMonitor) realized(ctx context.Context, position *LivePosition, since, until time.Time, force bool) (float64, bool) {
	if len(m.sources) == 0 {
		return 0, false
	}
	now := time.Now().UTC()
	lastSync := m.syncedAt[position.ID]
	reconcileTarget := until
	if force || lastSync.IsZero() || now.Sub(lastSync) >= fundingSyncInterval {
		lastAttempt := m.attemptedAt[position.ID]
		if !force && !lastAttempt.IsZero() && now.Sub(lastAttempt) < fundingSyncInterval {
			return 0, false
		}
		m.attemptedAt[position.ID] = now
		for _, venueName := range []string{position.VenueA, position.VenueB} {
			account := position.AccountBindings[venueName]
			source, ok := m.sources[venueName]
			if !ok {
				continue
			}
			if account == "" {
				return 0, false
			}
			for range maxFundingWindowsPerSync {
				window, needed, err := m.store.NextFundingWindow(ctx, position.ID, venueName, account, since, until)
				if err != nil {
					m.logger.Warn("funding monitor: plan history window", "err", err, "id", position.ID, "venue", venueName)
					return 0, false
				}
				if !needed {
					break
				}
				payments, err := source.FundingPayments(ctx, account, position.Asset, window.Since, window.Until)
				if err != nil {
					m.logger.Warn("funding monitor: fetch payments", "err", err, "id", position.ID, "venue", venueName)
					return 0, false
				}
				if err := m.store.ApplyFundingWindow(
					ctx, position.ID, venueName, account, position.Asset, since, window, payments,
				); err != nil {
					m.logger.Warn("funding monitor: persist history window", "err", err, "id", position.ID, "venue", venueName)
					return 0, false
				}
			}
		}
		m.syncedAt[position.ID] = now
		m.syncedTargets[position.ID] = until
	} else if target, ok := m.syncedTargets[position.ID]; ok {
		reconcileTarget = target
	}
	finalizing := force && position.State == string(ExecStateClosed)
	total, complete, err := m.store.ReconcileFunding(ctx, position, reconcileTarget, finalizing)
	if err != nil {
		m.logger.Warn("funding monitor: reconcile coverage", "err", err, "id", position.ID)
		return 0, false
	}
	return total, complete
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
		openedAt, openedErr := time.Parse(time.RFC3339, position.OpenedAt)
		if completedErr != nil || openedErr != nil || time.Since(completedAt) < fundingFinalizationDelay {
			continue
		}
		lastAttempt := m.finalizationAttemptedAt[position.ID]
		if !lastAttempt.IsZero() && time.Since(lastAttempt) < fundingSyncInterval {
			continue
		}
		m.finalizationAttemptedAt[position.ID] = time.Now()
		_, ok := m.realized(ctx, position, openedAt, completedAt, true)
		if !ok {
			continue
		}
	}
}
