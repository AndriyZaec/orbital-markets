package scanner

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
)

type openBlockedTestAdapter struct {
	*mutableScannerAdapter
	blocked map[string]string
}

func (a *openBlockedTestAdapter) OpenBlockedAssets() map[string]string { return a.blocked }

func TestOpenBlockedMarketsHideBothLegsButRemainFindable(t *testing.T) {
	a := &openBlockedTestAdapter{mutableScannerAdapter: &mutableScannerAdapter{name: "hyperliquid"}}
	s := New(slog.Default(), a)
	s.opps = []domain.Opportunity{
		{ID: "hl-a", Asset: "SAGA", VenuePair: domain.VenuePair{VenueA: "hyperliquid", VenueB: "aster"}},
		{ID: "hl-b", Asset: "SAGA", VenuePair: domain.VenuePair{VenueA: "aster", VenueB: "hyperliquid"}},
		{ID: "other", Asset: "SAGA", VenuePair: domain.VenuePair{VenueA: "aster", VenueB: "pacifica"}},
	}
	a.blocked = map[string]string{"saga": venue.OpenBlockOpenInterestCap}
	if got := s.Opportunities(); len(got) != 1 || got[0].ID != "other" {
		t.Fatalf("blocked opportunity published: %+v", got)
	}
	if s.FindOpportunity("hl-a") == nil {
		t.Fatal("FindOpportunity changed its inactive-asset semantics")
	}
	for _, id := range []string{"hl-a", "hl-b"} {
		_, err := s.BuildPlanWithLeverageCaps(context.Background(), id, 2, 100, nil)
		var blocked *OpenBlockedError
		if !errors.As(err, &blocked) || blocked.Reason != venue.OpenBlockOpenInterestCap {
			t.Fatalf("plan bypassed cap: %v", err)
		}
	}
	if len(s.InactiveAssets()) != 0 {
		t.Fatal("temporary cap became delisting")
	}
	a.blocked = nil
	if len(s.Opportunities()) != 3 {
		t.Fatal("lifting cap did not restore opportunities")
	}
}
