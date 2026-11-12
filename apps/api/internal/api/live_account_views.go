package api

import (
	"sort"
	"time"
)

// accountViews is the read-only acquisition path. Unlike execution contexts,
// it retains healthy feeds when another requested venue cannot start.
type accountViews struct {
	registry  *accountFeedRegistry
	bindings  map[string]string
	accounts  *liveAccountContext
	failures  map[string]string
	nextRetry time.Time
}

func newAccountViews(registry *accountFeedRegistry, bindings map[string]string) *accountViews {
	v := &accountViews{registry: registry, bindings: bindings,
		accounts: &liveAccountContext{leases: make(map[string]*accountFeedLease)}, failures: make(map[string]string)}
	v.retryMissing(time.Now())
	return v
}

// Only failed acquisitions are retried. Existing leases keep their subscribers
// and are released with the request; venue data readiness stays inside each feed.
func (v *accountViews) retryMissing(now time.Time) {
	if now.Before(v.nextRetry) {
		return
	}
	v.nextRetry = now.Add(5 * time.Second)
	venues := make([]string, 0, len(v.bindings))
	for venue := range v.bindings {
		venues = append(venues, venue)
	}
	sort.Strings(venues)
	for _, venue := range venues {
		if v.accounts.leases[venue] != nil {
			continue
		}
		lease, err := v.registry.Acquire(venue, v.bindings[venue])
		if err != nil {
			v.failures[venue] = err.Error()
			continue
		}
		v.accounts.leases[venue] = lease
		delete(v.failures, venue)
	}
}

func (v *accountViews) snapshots() map[string]liveAccountSnapshot {
	snapshots := make(map[string]liveAccountSnapshot, len(v.bindings))
	for venue, owner := range v.bindings {
		if feed, found := v.accounts.Feed(venue); found {
			snapshots[venue] = feed.Snapshot()
		} else {
			snapshots[venue] = liveAccountSnapshot{Venue: venue, Account: owner, UnavailableReason: v.failures[venue]}
		}
	}
	return snapshots
}

func accountViewStatuses(snapshots map[string]liveAccountSnapshot) map[string]venueAccountStatus {
	statuses := make(map[string]venueAccountStatus, len(snapshots))
	for venue, snapshot := range snapshots {
		statuses[venue] = buildVenueAccountStatusFromSnapshot(venue, snapshot, displayFreshness)
	}
	return statuses
}
