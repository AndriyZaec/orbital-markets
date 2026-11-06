package api

import (
	"context"
	"errors"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/helper"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/orders"
)

func (f *phoenixAccountFeed) RecoverySnapshot() liveAccountSnapshot {
	state := f.state.Snapshot()
	view := liveAccountSnapshot{Venue: "phoenix", Account: state.Owner, Connected: state.Connected, LastUpdated: state.ObservedAt}
	if f.ctx.Err() != nil || !state.Fresh(time.Now()) {
		view.UnavailableReason = "Phoenix recovery account is not fresh and synchronized"
		return view
	}
	markets := f.factory.config.Markets.Snapshot()
	for _, position := range state.Cross.Positions {
		if position.BasePositionLots != "0" && (!markets.Connected || !markets.Synchronized) {
			view.UnavailableReason = "Phoenix recovery position precision unavailable"
			return view
		}
	}
	positions, err := normalizePhoenixPositions(state, helper.RiskResult{LiquidationModel: "unavailable"}, markets)
	if err != nil {
		view.UnavailableReason = err.Error()
		return view
	}
	view.Positions, view.PositionsUpdatedAt = positions, state.PositionsUpdatedAt
	return view
}
func (f *phoenixAccountFeed) RefreshRecoveryPositions(ctx context.Context) error {
	return f.subscriber.Refresh(ctx)
}
func (f *phoenixAccountFeed) HasUnresolvedOrders(ctx context.Context) (bool, error) {
	if f.factory.orderJournal == nil {
		return false, nil
	}
	return f.factory.orderJournal.Unresolved(ctx, f.state.Snapshot().Owner)
}

func (f *phoenixAccountFeed) fillStateProof(ctx context.Context, minimum uint64) error {
	if err := f.subscriber.Refresh(ctx); err != nil {
		return err
	}
	state := f.state.Snapshot()
	if !state.Fresh(time.Now()) || state.Slot < minimum {
		return errors.New("Phoenix fill account state is not fresh at transaction slot")
	}
	return nil
}

// ReconcileOrder never sends. Missing journal evidence proves not-submitted for
// this backend, whose durable insert always precedes any broadcast.
func (f *phoenixAccountFeed) ReconcileOrder(ctx context.Context, q *domain.SigningRequest) (*normFill, error) {
	if f.factory.orders == nil {
		return nil, errPhoenixLiveDisabled
	}
	if q == nil || q.Account != f.state.Snapshot().Owner {
		return nil, orders.ErrConflict
	}
	if _, err := orders.Decode(q); err != nil {
		return nil, err
	}
	a, err := f.factory.orders.Reconcile(ctx, q.Account, q.ID, f.fillStateProof)
	if errors.Is(err, orders.ErrNotFound) {
		return &normFill{Status: "not_submitted"}, nil
	}
	if err != nil {
		return nil, err
	}
	return phoenixAttemptFill(a, q)
}

func (f *phoenixAccountFeed) ReconcileClose(ctx context.Context, cid string) (*domain.SigningRequest, *normFill, error) {
	if f.factory.orderJournal == nil || f.factory.orders == nil {
		return nil, nil, errPhoenixLiveDisabled
	}
	a, err := f.factory.orderJournal.ByClientID(ctx, f.state.Snapshot().Owner, cid)
	if errors.Is(err, orders.ErrNotFound) {
		return nil, &normFill{Status: "not_submitted"}, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if !a.Request.ReduceOnly || (a.Request.Action != "close" && a.Request.Action != "emergency_close") {
		return nil, nil, orders.ErrConflict
	}
	fill, err := f.ReconcileOrder(ctx, &a.Request)
	return &a.Request, fill, err
}

func (f *phoenixAccountFeed) ReconcileOutstandingOrders(ctx context.Context) error {
	if f.factory.orderJournal == nil || f.factory.orders == nil {
		return nil
	}
	attempts, err := f.factory.orderJournal.Pending(ctx, f.state.Snapshot().Owner, 4)
	if err != nil {
		return err
	}
	for _, a := range attempts {
		if _, lookupErr := f.ReconcileOrder(ctx, &a.Request); lookupErr != nil {
			err = errors.Join(err, lookupErr)
		}
		if ctx.Err() != nil {
			return errors.Join(err, ctx.Err())
		}
	}
	unaccounted, accountErr := f.factory.orderJournal.Unaccounted(ctx, f.state.Snapshot().Owner)
	if accountErr != nil {
		return errors.Join(err, accountErr)
	}
	for _, a := range unaccounted {
		_, accountErr := f.factory.orders.Account(ctx, a)
		err = errors.Join(err, accountErr)
		if ctx.Err() != nil {
			return errors.Join(err, ctx.Err())
		}
	}
	return err
}
