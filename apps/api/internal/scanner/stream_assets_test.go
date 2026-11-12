package scanner

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"testing"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
)

type streamAssetTestSink struct {
	*mutableScannerAdapter
	lists [][]string
}

func (a *streamAssetTestSink) SetCrossVenueAssets(assets []string) {
	a.lists = append(a.lists, append([]string{}, assets...))
}

func TestCollectPublishesOtherVenueAssetsBeforeHealthFiltering(t *testing.T) {
	aster := &streamAssetTestSink{mutableScannerAdapter: &mutableScannerAdapter{
		name: "aster", data: []venue.MarketData{{Venue: "aster", Asset: "LOCAL"}},
	}}
	hl := &mutableScannerAdapter{name: "hyperliquid", data: []venue.MarketData{
		{Venue: "hyperliquid", Asset: " btc "},
		{Venue: "hyperliquid", Asset: "BTC"},
		{Venue: "hyperliquid", Asset: "1000PEPE"},
	}}
	phoenix := &mutableScannerAdapter{name: "phoenix", data: []venue.MarketData{{Venue: "phoenix", Asset: " eth "}}}
	s := New(slog.New(slog.NewTextHandler(io.Discard, nil)), aster, hl, phoenix)
	s.collectByAsset(context.Background())
	if want := []string{"1000PEPE", "BTC", "ETH"}; len(aster.lists) != 1 || !reflect.DeepEqual(aster.lists[0], want) {
		t.Fatalf("other-venue assets = %v, want %v", aster.lists, want)
	}
	// A failed or empty venue changes only the new observation. The Aster sink
	// owns the lifetime union, as exercised by TestStreamAssetFilter.
	phoenix.err = errors.New("temporarily unavailable")
	hl.data = nil
	s.collectByAsset(context.Background())
	if len(aster.lists) != 2 || len(aster.lists[1]) != 0 {
		t.Fatalf("missing other venue still published assets: %v", aster.lists)
	}
	phoenix.err = nil
	s.collectByAsset(context.Background())
	if !reflect.DeepEqual(aster.lists[2], []string{"ETH"}) {
		t.Fatalf("recovered venue assets missing: %v", aster.lists)
	}
}
