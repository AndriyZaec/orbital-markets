package api

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/scanner"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
)

func writeOpenBlockedError(w http.ResponseWriter, err error) bool {
	var blocked *scanner.OpenBlockedError
	if !errors.As(err, &blocked) {
		return false
	}
	code := "OPEN_MARKET_BLOCKED"
	message := fmt.Sprintf("Opening %s on %s is temporarily unavailable. Choose another opportunity.", blocked.Asset, venueDisplayName(blocked.Venue))
	switch blocked.Reason {
	case venue.OpenBlockOpenInterestCap:
		message = fmt.Sprintf("%s isn't accepting new positions in %s right now. Choose another opportunity.", venueDisplayName(blocked.Venue), blocked.Asset)
	case venue.OpenBlockIsolatedOnly:
		message = fmt.Sprintf("%s only supports isolated positions in %s. Choose another opportunity.", venueDisplayName(blocked.Venue), blocked.Asset)
	case scanner.OpenBlockDataUnavailable:
		code = "OPEN_MARKET_STATUS_UNAVAILABLE"
		message = fmt.Sprintf("%s market availability is temporarily unavailable. Try again shortly.", venueDisplayName(blocked.Venue))
	}
	writeJSON(w, http.StatusUnprocessableEntity, map[string]string{
		"error": message, "code": code, "reason": blocked.Reason, "venue": blocked.Venue, "asset": blocked.Asset,
	})
	return true
}
