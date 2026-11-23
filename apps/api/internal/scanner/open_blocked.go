package scanner

import (
	"fmt"
	"strings"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
)

const OpenBlockDataUnavailable = "open_status_unavailable"

type OpenBlockedError struct {
	Venue, Asset, Reason string
}

func (e *OpenBlockedError) Error() string {
	return fmt.Sprintf("%s opening %s blocked: %s", e.Venue, e.Asset, e.Reason)
}

func (s *Scanner) OpenBlockedAssets() map[string]map[string]string {
	blocked := make(map[string]map[string]string)
	for _, adapter := range s.adapters {
		if source, ok := adapter.(venue.OpenBlockedAssetSource); ok {
			assets := make(map[string]string)
			for asset, reason := range source.OpenBlockedAssets() {
				assets[strings.ToUpper(asset)] = reason
			}
			blocked[adapter.Name()] = assets
		}
	}
	return blocked
}

// CheckOpenAvailability is an opening-only gate; it never fetches over the network.
func (s *Scanner) CheckOpenAvailability(asset string, venues ...string) error {
	for _, name := range venues {
		for _, adapter := range s.adapters {
			if adapter.Name() != name {
				continue
			}
			if source, ok := adapter.(venue.OpenBlockReadinessSource); ok && !source.OpenBlockDataReady() {
				return &OpenBlockedError{Venue: name, Asset: asset, Reason: OpenBlockDataUnavailable}
			}
		}
	}
	return s.checkKnownOpenBlocks(asset, venues...)
}

func (s *Scanner) checkKnownOpenBlocks(asset string, venues ...string) error {
	blocked := s.OpenBlockedAssets()
	for _, name := range venues {
		if reason, found := blocked[name][strings.ToUpper(asset)]; found {
			return &OpenBlockedError{Venue: name, Asset: asset, Reason: reason}
		}
	}
	return nil
}
