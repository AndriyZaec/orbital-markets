package aster

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
)

func TestRefreshBuildsHourlyUSDTPerpetualSnapshots(t *testing.T) {
	server, timestamp := newMarketServer(t)
	defer server.Close()
	adapter := newTestAdapter(server.URL)

	if err := adapter.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	snapshots, err := adapter.FetchMarketData(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 1 {
		t.Fatalf("snapshots = %+v, want one complete USDT perpetual", snapshots)
	}
	snapshot := snapshots[0]
	if snapshot.Venue != "aster" || snapshot.Asset != "BTC" || snapshot.MarketKey != "BTCUSDT" {
		t.Fatalf("identity = %+v", snapshot)
	}
	if snapshot.FundingRate != 0.0001 {
		t.Fatalf("hourly funding = %v, want 0.0001", snapshot.FundingRate)
	}
	if snapshot.BidSize != 200 || snapshot.AskSize != 303 {
		t.Fatalf("BBO notionals = %v/%v, want 200/303", snapshot.BidSize, snapshot.AskSize)
	}
	wantTime := time.UnixMilli(timestamp).UTC()
	if !snapshot.Timestamp.Equal(wantTime) {
		t.Fatalf("timestamp = %s, want %s", snapshot.Timestamp, wantTime)
	}
}

func TestFailedRefreshPreservesLastCompleteSnapshot(t *testing.T) {
	server, _ := newMarketServer(t)
	adapter := newTestAdapter(server.URL)
	if err := adapter.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	server.Close()

	if err := adapter.Refresh(context.Background()); err == nil {
		t.Fatal("refresh succeeded after test server closed")
	}
	snapshots, err := adapter.FetchMarketData(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 1 || snapshots[0].MarketKey != "BTCUSDT" {
		t.Fatalf("last complete snapshot was replaced: %+v", snapshots)
	}
}

func TestFetchMarketDataHidesStaleSnapshots(t *testing.T) {
	adapter := newTestAdapter("http://unused")
	adapter.snapshots["BTCUSDT"] = venue.MarketData{
		Venue: "aster", Asset: "BTC", MarketKey: "BTCUSDT",
		Timestamp: time.Now().Add(-maxSnapshotAge - time.Second),
	}
	snapshots, err := adapter.FetchMarketData(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 0 {
		t.Fatalf("stale snapshots returned: %+v", snapshots)
	}
}

func TestFundingParserAcceptsZeroAndRejectsNonFiniteValues(t *testing.T) {
	if value, ok := finiteDecimal("0"); !ok || value != 0 {
		t.Fatalf("zero funding parsed as %v, valid=%t", value, ok)
	}
	for _, value := range []string{"NaN", "+Inf", "invalid"} {
		if _, ok := finiteDecimal(value); ok {
			t.Fatalf("non-finite funding %q was accepted", value)
		}
	}
}

func newTestAdapter(restURL string) *Adapter {
	return &Adapter{
		snapshots: make(map[string]venue.MarketData),
		logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		client:    &http.Client{Timeout: time.Second}, restURL: restURL,
	}
}

func newMarketServer(t *testing.T) (*httptest.Server, int64) {
	t.Helper()
	timestamp := time.Now().Add(-time.Second).UnixMilli()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/fapi/v3/exchangeInfo":
			_, _ = response.Write([]byte(`{"symbols":[{"symbol":"BTCUSDT","contractType":"PERPETUAL","status":"TRADING","baseAsset":"BTC","quoteAsset":"USDT"},{"symbol":"ETHUSDC","contractType":"PERPETUAL","status":"TRADING","baseAsset":"ETH","quoteAsset":"USDC"}]}`))
		case "/fapi/v3/premiumIndex":
			_, _ = fmt.Fprintf(response, `[{"symbol":"BTCUSDT","markPrice":"100","indexPrice":"100","lastFundingRate":"0.0008","time":%d}]`, timestamp+1)
		case "/fapi/v3/ticker/bookTicker":
			_, _ = fmt.Fprintf(response, `[{"symbol":"BTCUSDT","bidPrice":"100","bidQty":"2","askPrice":"101","askQty":"3","time":%d}]`, timestamp)
		case "/fapi/v3/fundingInfo":
			_, _ = response.Write([]byte(`[{"symbol":"BTCUSDT","fundingIntervalHours":8}]`))
		default:
			http.NotFound(response, request)
		}
	}))
	return server, timestamp
}
