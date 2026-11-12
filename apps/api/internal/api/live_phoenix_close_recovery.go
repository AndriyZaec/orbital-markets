package api

import (
	"context"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/executor"
)

type outstandingOrderReconciler interface{ ReconcileOutstandingOrders(context.Context) error }
type closeOrderReconciler interface {
	ReconcileClose(context.Context, string) (*domain.SigningRequest, *normFill, error)
}

// A fast close can finish before the next background fee read. Keep known
// closed Phoenix owners in the bounded finalization pass, not only open feeds.
func (s *Server) refreshUnfinalizedPhoenixAccounting() {
	if s.live == nil || s.live.accounts == nil || s.liveStore == nil {
		return
	}
	positions, err := s.liveStore.ListUnfinalizedClosedPositions(s.ctx)
	if err != nil {
		return
	}
	seen := map[string]bool{}
	for i := range positions {
		position := &positions[i]
		owner := position.AccountBindings["phoenix"]
		if owner == "" {
			continue
		}
		if !seen[owner] {
			s.reconcilePhoenixPositionOrders(s.ctx, position)
			seen[owner] = true
		} else {
			_ = s.liveStore.SyncPhoenixObservedFees(s.ctx, position)
		}
	}
}

func (s *Server) reconcilePhoenixPositionOrders(ctx context.Context, position *executor.LivePosition) {
	owner := position.AccountBindings["phoenix"]
	if owner == "" || s.live == nil || s.live.accounts == nil {
		return
	}
	lease, err := s.live.accounts.AcquireRecovery("phoenix", owner)
	if err != nil {
		return
	}
	defer lease.Release()
	if source, ok := lease.Feed().(outstandingOrderReconciler); ok {
		lookup, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		_ = source.ReconcileOutstandingOrders(lookup) // Uncertain remains durable.
		_ = s.liveStore.SyncPhoenixObservedFees(lookup, position)
	}
}

func (s *Server) reconcilePhoenixCloseOutcomes(ctx context.Context, position *executor.LivePosition) {
	owner := position.AccountBindings["phoenix"]
	if owner == "" || s.live == nil || s.live.accounts == nil {
		return
	}
	rows, err := s.liveStore.PendingPhoenixCloses(ctx, position.ID)
	if err != nil || len(rows) == 0 {
		return
	}
	lease, err := s.live.accounts.AcquireRecovery("phoenix", owner)
	if err != nil {
		return
	}
	defer lease.Release()
	source, ok := lease.Feed().(closeOrderReconciler)
	if !ok {
		return
	}
	for _, outcome := range rows {
		request, fill, err := source.ReconcileClose(ctx, outcome.ClientOrderID)
		if err != nil || fill == nil {
			continue
		}
		if request != nil && (request.PositionID != position.ID || request.Leg != outcome.Leg || request.Account != owner || request.ClientOrderID != outcome.ClientOrderID) {
			continue
		}
		outcome.Resolved = true
		outcome.OrderID = fill.OrderID
		outcome.Accepted = fill.OrderID != ""
		outcome.FilledAmount, outcome.AvgFillPrice = fill.FilledAmount, fill.AvgFillPrice
		if outcome.RequestedAmount > 0 {
			outcome.FillRatio = fill.FilledAmount / outcome.RequestedAmount
		}
		outcome.Confirmed = fill.Filled && outcome.FillRatio >= closeFillThreshold
		outcome.Error = ""
		if !outcome.Confirmed {
			outcome.Error = "Phoenix exact close resolved with residual/no execution; fresh signing required"
		}
		if err := s.liveStore.UpsertCloseOutcome(ctx, outcome); err != nil {
			return
		}
		if !outcome.Confirmed {
			if progress, err := s.liveStore.GetCloseProgress(ctx, position.ID); err == nil && progress.Pending == 0 {
				_ = s.markCloseDegraded(ctx, position.ID)
			}
		}
	}
}
