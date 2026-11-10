package domain

import "slices"

// This is the shared policy allowlist, not proof that a particular deployment
// has a configured execution module or that an account passes live readiness.
var liveVenues = [...]string{"pacifica", "hyperliquid", "aster", "phoenix"}

func SupportedLiveVenues() []string {
	return append([]string(nil), liveVenues[:]...)
}

func IsSupportedLiveVenue(venue string) bool {
	return slices.Contains(liveVenues[:], venue)
}
