package api

import (
	"testing"
	"time"
)

func TestVenuePositionStateReadyAcceptsQuietInitializedAccount(t *testing.T) {
	updatedAt := time.Now().Add(-time.Hour)
	if !positionStateReady(updatedAt, time.Time{}) {
		t.Fatal("initialized quiet position state should remain ready")
	}
}

func TestVenuePositionStateReadyAfterRequiresFreshReconciliation(t *testing.T) {
	after := time.Now().Add(-time.Minute)
	if positionStateReady(time.Now().Add(-time.Hour), after) {
		t.Fatal("stale position state should not satisfy recovery reconciliation")
	}
}
