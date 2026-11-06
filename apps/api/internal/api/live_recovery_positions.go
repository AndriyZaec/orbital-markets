package api

import "context"

// Recovery position truth must not depend on opening margin, collateral price,
// or delegation readiness. Existing venues retain their ordinary snapshot path.
type recoveryPositionSource interface {
	RecoverySnapshot() liveAccountSnapshot
	RefreshRecoveryPositions(context.Context) error
}

func recoveryPositionSnapshot(feed liveAccountFeed) liveAccountSnapshot {
	if source, ok := feed.(recoveryPositionSource); ok {
		return source.RecoverySnapshot()
	}
	return feed.Snapshot()
}
func refreshRecoveryPositions(ctx context.Context, feed liveAccountFeed) error {
	if source, ok := feed.(recoveryPositionSource); ok {
		return source.RefreshRecoveryPositions(ctx)
	}
	return feed.RefreshPositions(ctx)
}

type unresolvedOrderSource interface {
	HasUnresolvedOrders(context.Context) (bool, error)
}

// A currently flat account is not proof that an already-broadcast order cannot
// land later. Unknown journal state must not finalize flat/closed recovery.
func recoveryOrdersUnresolved(ctx context.Context, accounts *liveAccountContext) (bool, error) {
	if accounts == nil {
		return false, nil
	}
	for _, lease := range accounts.leases {
		if source, ok := lease.Feed().(unresolvedOrderSource); ok {
			pending, err := source.HasUnresolvedOrders(ctx)
			if err != nil || pending {
				return true, err
			}
		}
	}
	return false, nil
}
