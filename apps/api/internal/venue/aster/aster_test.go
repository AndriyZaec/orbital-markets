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

	"github.com/gorilla/websocket"
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
	rules, ok := adapter.OrderRules("BTCUSDT")
	if !ok {
		t.Fatal("BTCUSDT order rules unavailable")
	}
	if rules.TickSize != "0.10" || rules.QuantityStep != "0.001" || rules.MinNotional != "5" {
		t.Fatalf("order rules = %+v", rules)
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
	adapter.markets["BTCUSDT"] = marketState{
		marketMetadata: marketMetadata{asset: "BTC", fundingIntervalHours: 8},
		markPrice:      100, indexPrice: 100, bidPrice: 99, bidSize: 100, askPrice: 101, askSize: 100,
		markUpdatedAt: time.Now().Add(-maxSnapshotAge - time.Second),
		bookUpdatedAt: time.Now(),
	}
	snapshots, err := adapter.FetchMarketData(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 0 {
		t.Fatalf("stale snapshots returned: %+v", snapshots)
	}
}

func TestStreamUpdatesPreserveIndependentFreshnessAndOrder(t *testing.T) {
	server, _ := newMarketServer(t)
	defer server.Close()
	adapter := newTestAdapter(server.URL)
	if err := adapter.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	timestamp := time.Now().UnixMilli()
	mark := fmt.Sprintf(`{"stream":"!markPrice@arr@1s","data":[{"E":%d,"s":"BTCUSDT","p":"110","i":"109","r":"0.0016"}]}`, timestamp)
	book := fmt.Sprintf(`{"stream":"!bookTicker","data":{"u":11,"E":%d,"T":%d,"s":"BTCUSDT","b":"109","B":"4","a":"110","A":"5"}}`, timestamp+2, timestamp+1)
	if err := adapter.applyStreamMessage([]byte(mark)); err != nil {
		t.Fatal(err)
	}
	if err := adapter.applyStreamMessage([]byte(book)); err != nil {
		t.Fatal(err)
	}
	olderMark := fmt.Sprintf(`{"stream":"!markPrice@arr@1s","data":[{"E":%d,"s":"BTCUSDT","p":"1","i":"1","r":"0"}]}`, timestamp-1)
	if err := adapter.applyStreamMessage([]byte(olderMark)); err != nil {
		t.Fatal(err)
	}
	sameMillisecondBook := fmt.Sprintf(`{"stream":"!bookTicker","data":{"u":12,"E":%d,"T":%d,"s":"BTCUSDT","b":"108","B":"4","a":"111","A":"5"}}`, timestamp+2, timestamp+1)
	if err := adapter.applyStreamMessage([]byte(sameMillisecondBook)); err != nil {
		t.Fatal(err)
	}
	olderBook := fmt.Sprintf(`{"stream":"!bookTicker","data":{"u":11,"E":%d,"T":%d,"s":"BTCUSDT","b":"1","B":"1","a":"2","A":"1"}}`, timestamp+20, timestamp+20)
	if err := adapter.applyStreamMessage([]byte(olderBook)); err != nil {
		t.Fatal(err)
	}

	snapshots, err := adapter.FetchMarketData(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 1 {
		t.Fatalf("snapshots = %+v, want updated BTC", snapshots)
	}
	snapshot := snapshots[0]
	if snapshot.MarkPrice != 110 || snapshot.IndexPrice != 109 || snapshot.FundingRate != 0.0002 {
		t.Fatalf("mark update = %+v", snapshot)
	}
	if snapshot.BidSize != 432 || snapshot.AskSize != 555 {
		t.Fatalf("book update notionals = %v/%v", snapshot.BidSize, snapshot.AskSize)
	}
	if !snapshot.Timestamp.Equal(time.UnixMilli(timestamp)) {
		t.Fatalf("combined timestamp = %s", snapshot.Timestamp)
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
		markets: make(map[string]marketState),
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		client:  &http.Client{Timeout: time.Second}, restURL: restURL,
		wsDialer: websocket.DefaultDialer,
	}
}

func newMarketServer(t *testing.T) (*httptest.Server, int64) {
	t.Helper()
	timestamp := time.Now().Add(-time.Second).UnixMilli()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/fapi/v3/exchangeInfo":
			_, _ = response.Write([]byte(`{"symbols":[{"symbol":"BTCUSDT","contractType":"PERPETUAL","status":"TRADING","baseAsset":"BTC","quoteAsset":"USDT","filters":[{"filterType":"PRICE_FILTER","minPrice":"0.10","maxPrice":"1000000","tickSize":"0.10"},{"filterType":"LOT_SIZE","minQty":"0.001","maxQty":"1000","stepSize":"0.001"},{"filterType":"MARKET_LOT_SIZE","minQty":"0.01","maxQty":"100","stepSize":"0.01"},{"filterType":"MIN_NOTIONAL","notional":"5"}]},{"symbol":"ETHUSDC","contractType":"PERPETUAL","status":"TRADING","baseAsset":"ETH","quoteAsset":"USDC"}]}`))
		case "/fapi/v3/premiumIndex":
			_, _ = fmt.Fprintf(response, `[{"symbol":"BTCUSDT","markPrice":"100","indexPrice":"100","lastFundingRate":"0.0008","time":%d}]`, timestamp+1)
		case "/fapi/v3/ticker/bookTicker":
			_, _ = fmt.Fprintf(response, `[{"lastUpdateId":10,"symbol":"BTCUSDT","bidPrice":"100","bidQty":"2","askPrice":"101","askQty":"3","time":%d}]`, timestamp)
		case "/fapi/v3/fundingInfo":
			_, _ = response.Write([]byte(`[{"symbol":"BTCUSDT","fundingIntervalHours":8}]`))
		default:
			http.NotFound(response, request)
		}
	}))
	return server, timestamp
}
