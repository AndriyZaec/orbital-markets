package aster

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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
	if snapshot.OpenInterest != 6013.199 {
		t.Fatalf("open interest = %v, want 6013.199", snapshot.OpenInterest)
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

func TestFetchMarketDataKeepsUnchangedBookWhileStreamIsHealthy(t *testing.T) {
	now := time.Date(2026, time.September, 24, 12, 0, 0, 0, time.UTC)
	adapter := newTestAdapter("http://unused")
	adapter.now = func() time.Time { return now }
	adapter.streamGeneration = 7
	adapter.streamConnected = true
	adapter.streamSynchronized = true
	adapter.lastStreamReceivedAt = now
	adapter.markets["PIPPINUSDT"] = marketState{
		marketMetadata: marketMetadata{asset: "PIPPIN", fundingIntervalHours: 8},
		markPrice:      0.5, indexPrice: 0.5, nativeFundingRate: 0.0008,
		bidPrice: 0.49, bidSize: 100, askPrice: 0.51, askSize: 100,
		markUpdatedAt:    now,
		indexUpdatedAt:   now,
		fundingUpdatedAt: now,
		bookUpdatedAt:    now.Add(-time.Hour),
		bookGeneration:   7,
	}

	snapshots, err := adapter.FetchMarketData(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 1 || snapshots[0].MarketKey != "PIPPINUSDT" {
		t.Fatalf("snapshots = %+v, want quiet PIPPIN book while stream is healthy", snapshots)
	}
	if !snapshots[0].Timestamp.Equal(now) {
		t.Fatalf("snapshot timestamp = %s, want stream-observed %s", snapshots[0].Timestamp, now)
	}

	adapter.streamGeneration = 8
	snapshots, err = adapter.FetchMarketData(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 0 {
		t.Fatalf("previous stream generation returned snapshots: %+v", snapshots)
	}

	adapter.streamGeneration = 7
	adapter.streamSynchronized = false
	snapshots, err = adapter.FetchMarketData(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 0 {
		t.Fatalf("disconnected stream returned snapshots: %+v", snapshots)
	}
}

func TestRefreshPreservesMarketOmittedFromPartialRESTSnapshot(t *testing.T) {
	serverState := &mutableMarketServerState{}
	server := newMutableMarketServer(t, serverState)
	defer server.Close()
	adapter := newTestAdapter(server.URL)
	adapter.now = func() time.Time { return mutableMarketNow }

	if err := adapter.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	serverState.omitETHBook.Store(true)
	if err := adapter.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := adapter.markets["ETHUSDT"]; !ok {
		t.Fatal("partial REST snapshot deleted known ETHUSDT market")
	}
	market := adapter.markets["ETHUSDT"]
	if market.markPrice != 210 || market.bidPrice != 199 {
		t.Fatalf("partial ETHUSDT reconciliation = mark %v bid %v, want new mark and preserved book", market.markPrice, market.bidPrice)
	}
	snapshots, err := adapter.FetchMarketData(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 2 {
		t.Fatalf("snapshots = %+v, want previous ETHUSDT observation preserved", snapshots)
	}
}

func TestRefreshPreservesMarketOmittedFromPartialMetadata(t *testing.T) {
	serverState := &mutableMarketServerState{}
	server := newMutableMarketServer(t, serverState)
	defer server.Close()
	adapter := newTestAdapter(server.URL)
	adapter.now = func() time.Time { return mutableMarketNow }

	if err := adapter.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	serverState.omitETHMetadata.Store(true)
	if err := adapter.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := adapter.markets["ETHUSDT"]; !ok {
		t.Fatal("partial metadata response deleted known ETHUSDT market")
	}
}

func TestRefreshRemovesExplicitlyInactiveMarket(t *testing.T) {
	serverState := &mutableMarketServerState{}
	server := newMutableMarketServer(t, serverState)
	defer server.Close()
	adapter := newTestAdapter(server.URL)
	adapter.now = func() time.Time { return mutableMarketNow }

	if err := adapter.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	serverState.inactiveETH.Store(true)
	if err := adapter.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := adapter.markets["ETHUSDT"]; ok {
		t.Fatal("explicitly inactive ETHUSDT market was preserved")
	}
}

func TestStreamRecoversKnownMarketMissingFromRESTComponents(t *testing.T) {
	now := mutableMarketNow
	serverState := &mutableMarketServerState{}
	serverState.omitETHBook.Store(true)
	server := newMutableMarketServer(t, serverState)
	defer server.Close()
	adapter := newTestAdapter(server.URL)
	adapter.now = func() time.Time { return now }

	if err := adapter.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if state, ok := adapter.markets["ETHUSDT"]; !ok || state.asset != "ETH" {
		t.Fatalf("known ETHUSDT metadata = %+v, present=%t", state, ok)
	}
	adapter.streamGeneration = 1
	adapter.streamConnected = true
	adapter.streamSynchronized = true
	adapter.lastStreamReceivedAt = now
	timestamp := now.UnixMilli()
	mark := fmt.Sprintf(`{"stream":"!markPrice@arr@1s","data":[{"E":%d,"s":"ETHUSDT","p":"200","i":"200","r":"0.0008"}]}`, timestamp)
	book := fmt.Sprintf(`{"stream":"!bookTicker","data":{"u":11,"E":%d,"T":%d,"s":"ETHUSDT","b":"199","B":"2","a":"201","A":"3"}}`, timestamp, timestamp)
	if err := adapter.applyStreamMessage([]byte(mark)); err != nil {
		t.Fatal(err)
	}
	if err := adapter.applyStreamMessage([]byte(book)); err != nil {
		t.Fatal(err)
	}

	snapshots, err := adapter.FetchMarketData(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, snapshot := range snapshots {
		if snapshot.MarketKey == "ETHUSDT" && snapshot.MarkPrice == 200 && snapshot.BidPrice == 199 {
			return
		}
	}
	t.Fatalf("stream did not recover ETHUSDT observation: %+v", snapshots)
}

func TestRefreshReplaysStreamUpdatesReceivedBeforeMetadata(t *testing.T) {
	serverState := &mutableMarketServerState{}
	serverState.omitETHBook.Store(true)
	server := newMutableMarketServer(t, serverState)
	defer server.Close()
	adapter := newTestAdapter(server.URL)
	adapter.now = func() time.Time { return mutableMarketNow }
	adapter.streamGeneration = 1
	adapter.streamConnected = true
	timestamp := mutableMarketNow.UnixMilli()
	mark := fmt.Sprintf(`{"stream":"!markPrice@arr@1s","data":[{"E":%d,"s":"ETHUSDT","p":"220","i":"220","r":"0.0008"}]}`, timestamp)
	book := fmt.Sprintf(`{"stream":"!bookTicker","data":{"u":20,"E":%d,"T":%d,"s":"ETHUSDT","b":"219","B":"2","a":"221","A":"3"}}`, timestamp, timestamp)
	if err := adapter.applyStreamMessage([]byte(mark)); err != nil {
		t.Fatal(err)
	}
	if err := adapter.applyStreamMessage([]byte(book)); err != nil {
		t.Fatal(err)
	}
	if len(adapter.pendingMarks) != 1 || len(adapter.pendingBooks) != 1 {
		t.Fatalf("pending stream updates = %d/%d, want one mark and book", len(adapter.pendingMarks), len(adapter.pendingBooks))
	}

	if err := adapter.refresh(context.Background(), refreshToken{
		expectedGeneration: 1,
		bookGeneration:     1,
	}); err != nil {
		t.Fatal(err)
	}
	adapter.streamSynchronized = true
	snapshots, err := adapter.FetchMarketData(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, snapshot := range snapshots {
		if snapshot.MarketKey == "ETHUSDT" && snapshot.MarkPrice == 220 && snapshot.BidPrice == 219 {
			return
		}
	}
	t.Fatalf("pre-metadata stream updates were not replayed: %+v", snapshots)
}

func TestOpenInterestFailureDoesNotDeleteValidMarket(t *testing.T) {
	serverState := &mutableMarketServerState{}
	serverState.failETHOpenInterest.Store(true)
	server := newMutableMarketServer(t, serverState)
	defer server.Close()
	adapter := newTestAdapter(server.URL)

	if err := adapter.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	market, ok := adapter.markets["ETHUSDT"]
	if !ok {
		t.Fatal("open-interest failure deleted ETHUSDT market")
	}
	if market.openInterestKnown || market.openInterest != 0 {
		t.Fatalf("ETHUSDT open interest = %v, known=%t, want unavailable component", market.openInterest, market.openInterestKnown)
	}
}

func TestRefreshStartedBeforeReconnectCannotReplaceNewGeneration(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	server := newBlockingMarketServer(t, started, release)
	defer server.Close()
	adapter := newTestAdapter(server.URL)
	adapter.now = func() time.Time { return mutableMarketNow }
	adapter.streamGeneration = 1
	adapter.streamConnected = true
	adapter.streamSynchronized = true
	adapter.markets["BTCUSDT"] = marketState{
		marketMetadata: marketMetadata{asset: "BTC", fundingIntervalHours: 8},
		markPrice:      123,
		markUpdatedAt:  mutableMarketNow,
		bookUpdatedAt:  mutableMarketNow,
		bookGeneration: 1,
	}

	done := make(chan error, 1)
	go func() { done <- adapter.Refresh(context.Background()) }()
	<-started
	adapter.mu.Lock()
	adapter.streamGeneration = 2
	adapter.streamSynchronized = false
	adapter.mu.Unlock()
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if state := adapter.markets["BTCUSDT"]; state.markPrice != 123 || state.bookGeneration != 1 {
		t.Fatalf("superseded refresh replaced newer generation state: %+v", state)
	}
}

func TestConnectAndListenReplaysUpdatesBeforePublishingHealthy(t *testing.T) {
	releaseREST := make(chan struct{})
	releaseWebSocket := make(chan struct{})
	upgrader := websocket.Upgrader{}
	timestamp := mutableMarketNow.UnixMilli()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/ws":
			connection, err := upgrader.Upgrade(response, request, nil)
			if err != nil {
				t.Errorf("upgrade websocket: %v", err)
				return
			}
			defer connection.Close()
			mark := fmt.Sprintf(`{"stream":"!markPrice@arr@1s","data":[{"E":%d,"s":"PIPPINUSDT","p":"0.5","i":"0.51","r":"0.0008"}]}`, timestamp)
			book := fmt.Sprintf(`{"stream":"!bookTicker","data":{"u":20,"E":%d,"T":%d,"s":"PIPPINUSDT","b":"0.49","B":"200","a":"0.51","A":"200"}}`, timestamp, timestamp)
			if err := connection.WriteMessage(websocket.TextMessage, []byte(mark)); err != nil {
				t.Errorf("write mark message: %v", err)
				return
			}
			if err := connection.WriteMessage(websocket.TextMessage, []byte(book)); err != nil {
				t.Errorf("write book message: %v", err)
				return
			}
			<-releaseWebSocket
		case "/fapi/v3/exchangeInfo":
			<-releaseREST
			_, _ = response.Write([]byte(`{"symbols":[{"symbol":"PIPPINUSDT","contractType":"PERPETUAL","status":"TRADING","baseAsset":"PIPPIN","quoteAsset":"USDT"}]}`))
		case "/fapi/v3/fundingInfo":
			_, _ = response.Write([]byte(`[{"symbol":"PIPPINUSDT","fundingIntervalHours":8}]`))
		case "/fapi/v3/premiumIndex":
			_, _ = response.Write([]byte(`[]`))
		case "/fapi/v3/ticker/bookTicker":
			_, _ = response.Write([]byte(`[]`))
		case "/fapi/v3/openInterest":
			_, _ = response.Write([]byte(`{"symbol":"PIPPINUSDT","openInterest":"1000"}`))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	events := make(chan string, 64)
	adapter := newTestAdapter(server.URL)
	adapter.logger = slog.New(channelLogHandler{events: events})
	adapter.now = func() time.Time { return mutableMarketNow }
	adapter.wsURL = "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- adapter.connectAndListen(ctx) }()

	waitForLog(t, events, "aster stream update for unknown symbol")
	close(releaseREST)
	waitForLog(t, events, "aster market websocket health changed")
	snapshots, err := adapter.FetchMarketData(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 1 || snapshots[0].MarketKey != "PIPPINUSDT" || snapshots[0].BidPrice != 0.49 {
		t.Fatalf("healthy synchronized snapshots = %+v, want replayed PIPPIN", snapshots)
	}

	cancel()
	close(releaseWebSocket)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("connectAndListen did not stop after cancellation")
	}
}

func TestUnsynchronizedRefreshCannotPublishAfterGenerationBecomesHealthy(t *testing.T) {
	adapter := newTestAdapter("http://unused")
	adapter.streamGeneration = 2
	adapter.streamConnected = true
	adapter.streamSynchronized = true
	adapter.markets["PIPPINUSDT"] = marketState{
		marketMetadata: marketMetadata{asset: "PIPPIN", fundingIntervalHours: 8},
		markPrice:      0.5,
		bookGeneration: 2,
	}

	err := adapter.refresh(context.Background(), refreshToken{
		expectedGeneration: 2,
		bookGeneration:     0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if state := adapter.markets["PIPPINUSDT"]; state.markPrice != 0.5 || state.bookGeneration != 2 {
		t.Fatalf("superseded unsynchronized refresh replaced healthy state: %+v", state)
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
	mark := fmt.Sprintf(`{"stream":"!markPrice@arr@1s","data":[{"e":"markPriceUpdate","E":%d,"s":"BTCUSDT","p":"110","P":"999","i":"109","r":"0.0016"}]}`, timestamp)
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

func TestMarkStreamUpdatesValidComponentsIndependently(t *testing.T) {
	now := mutableMarketNow
	previous := now.Add(-time.Second)
	adapter := newTestAdapter("http://unused")
	adapter.markets["PIPPINUSDT"] = marketState{
		marketMetadata:    marketMetadata{asset: "PIPPIN", fundingIntervalHours: 8},
		markPrice:         0.4,
		indexPrice:        0.4,
		nativeFundingRate: 0.0008,
		markUpdatedAt:     previous,
		indexUpdatedAt:    previous,
		fundingUpdatedAt:  previous,
	}
	message := fmt.Sprintf(`{"stream":"!markPrice@arr@1s","data":[{"E":%d,"s":"PIPPINUSDT","p":"0.5","i":"0.51","r":"NaN"}]}`, now.UnixMilli())

	if err := adapter.applyStreamMessage([]byte(message)); err != nil {
		t.Fatal(err)
	}
	state := adapter.markets["PIPPINUSDT"]
	if state.markPrice != 0.5 || state.indexPrice != 0.51 {
		t.Fatalf("valid mark/index components were not updated: %+v", state)
	}
	if state.nativeFundingRate != 0.0008 || !state.fundingUpdatedAt.Equal(previous) {
		t.Fatalf("invalid funding component replaced previous observation: %+v", state)
	}
}

func TestBookTickerStreamSeparatesEventTypeFromEventTime(t *testing.T) {
	adapter := newTestAdapter("http://unused")
	adapter.markets["BTCUSDT"] = marketState{
		marketMetadata: marketMetadata{asset: "BTC", fundingIntervalHours: 8},
	}
	const timestamp int64 = 1788727432100
	message := `{"stream":"!bookTicker","data":{"e":"bookTicker","u":530721740845,"s":"BTCUSDT","b":"109","B":"4","a":"110","A":"5","T":1788727432100,"E":1788727432119}}`

	if err := adapter.applyStreamMessage([]byte(message)); err != nil {
		t.Fatalf("Aster book ticker rejected: %v", err)
	}
	state := adapter.markets["BTCUSDT"]
	if state.bookUpdateID != 530721740845 || !state.bookUpdatedAt.Equal(time.UnixMilli(timestamp)) {
		t.Fatalf("book sequence/time = %d/%s", state.bookUpdateID, state.bookUpdatedAt)
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
		markets:              make(map[string]marketState),
		logger:               slog.New(slog.NewTextHandler(io.Discard, nil)),
		client:               &http.Client{Timeout: time.Second},
		restURL:              restURL,
		wsDialer:             websocket.DefaultDialer,
		now:                  time.Now,
		unknownStreamSymbols: make(map[string]struct{}),
		pendingMarks:         make(map[string]marketState),
		pendingBooks:         make(map[string]marketState),
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
		case "/fapi/v3/openInterest":
			if request.URL.Query().Get("symbol") != "BTCUSDT" {
				t.Fatalf("open interest symbol = %q", request.URL.Query().Get("symbol"))
			}
			_, _ = response.Write([]byte(`{"symbol":"BTCUSDT","openInterest":"6013.199","time":1788728114624}`))
		default:
			http.NotFound(response, request)
		}
	}))
	return server, timestamp
}

type mutableMarketServerState struct {
	omitETHBook         atomic.Bool
	omitETHMetadata     atomic.Bool
	inactiveETH         atomic.Bool
	failETHOpenInterest atomic.Bool
}

func newMutableMarketServer(t *testing.T, state *mutableMarketServerState) *httptest.Server {
	t.Helper()
	timestamp := mutableMarketNow.Add(-time.Second).UnixMilli()
	return httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/fapi/v3/exchangeInfo":
			status := "TRADING"
			if state.inactiveETH.Load() {
				status = "BREAK"
			}
			if state.omitETHMetadata.Load() {
				_, _ = response.Write([]byte(`{"symbols":[{"symbol":"BTCUSDT","contractType":"PERPETUAL","status":"TRADING","baseAsset":"BTC","quoteAsset":"USDT"}]}`))
				return
			}
			_, _ = fmt.Fprintf(response, `{"symbols":[{"symbol":"BTCUSDT","contractType":"PERPETUAL","status":"TRADING","baseAsset":"BTC","quoteAsset":"USDT"},{"symbol":"ETHUSDT","contractType":"PERPETUAL","status":"%s","baseAsset":"ETH","quoteAsset":"USDT"}]}`, status)
		case "/fapi/v3/fundingInfo":
			_, _ = response.Write([]byte(`[{"symbol":"BTCUSDT","fundingIntervalHours":8},{"symbol":"ETHUSDT","fundingIntervalHours":8}]`))
		case "/fapi/v3/premiumIndex":
			ethMark := "200"
			ethTimestamp := timestamp
			if state.omitETHBook.Load() {
				ethMark = "210"
				ethTimestamp++
			}
			_, _ = fmt.Fprintf(response, `[{"symbol":"BTCUSDT","markPrice":"100","indexPrice":"100","lastFundingRate":"0.0008","time":%d},{"symbol":"ETHUSDT","markPrice":"%s","indexPrice":"%s","lastFundingRate":"0.0008","time":%d}]`, timestamp, ethMark, ethMark, ethTimestamp)
		case "/fapi/v3/ticker/bookTicker":
			if state.omitETHBook.Load() {
				_, _ = fmt.Fprintf(response, `[{"lastUpdateId":10,"symbol":"BTCUSDT","bidPrice":"99","bidQty":"2","askPrice":"101","askQty":"3","time":%d}]`, timestamp)
				return
			}
			_, _ = fmt.Fprintf(response, `[{"lastUpdateId":10,"symbol":"BTCUSDT","bidPrice":"99","bidQty":"2","askPrice":"101","askQty":"3","time":%d},{"lastUpdateId":10,"symbol":"ETHUSDT","bidPrice":"199","bidQty":"2","askPrice":"201","askQty":"3","time":%d}]`, timestamp, timestamp)
		case "/fapi/v3/openInterest":
			symbol := request.URL.Query().Get("symbol")
			if symbol == "ETHUSDT" && state.failETHOpenInterest.Load() {
				http.Error(response, "unavailable", http.StatusServiceUnavailable)
				return
			}
			_, _ = fmt.Fprintf(response, `{"symbol":"%s","openInterest":"1000"}`, symbol)
		default:
			http.NotFound(response, request)
		}
	}))
}

var mutableMarketNow = time.Date(2026, time.September, 24, 12, 0, 0, 0, time.UTC)

func newBlockingMarketServer(t *testing.T, started chan<- struct{}, release <-chan struct{}) *httptest.Server {
	t.Helper()
	timestamp := mutableMarketNow.Add(-time.Second).UnixMilli()
	return httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/fapi/v3/exchangeInfo":
			close(started)
			<-release
			_, _ = response.Write([]byte(`{"symbols":[{"symbol":"BTCUSDT","contractType":"PERPETUAL","status":"TRADING","baseAsset":"BTC","quoteAsset":"USDT"}]}`))
		case "/fapi/v3/fundingInfo":
			_, _ = response.Write([]byte(`[{"symbol":"BTCUSDT","fundingIntervalHours":8}]`))
		case "/fapi/v3/premiumIndex":
			_, _ = fmt.Fprintf(response, `[{"symbol":"BTCUSDT","markPrice":"100","indexPrice":"100","lastFundingRate":"0.0008","time":%d}]`, timestamp)
		case "/fapi/v3/ticker/bookTicker":
			_, _ = fmt.Fprintf(response, `[{"lastUpdateId":10,"symbol":"BTCUSDT","bidPrice":"99","bidQty":"2","askPrice":"101","askQty":"3","time":%d}]`, timestamp)
		case "/fapi/v3/openInterest":
			_, _ = response.Write([]byte(`{"symbol":"BTCUSDT","openInterest":"1000"}`))
		default:
			http.NotFound(response, request)
		}
	}))
}

type channelLogHandler struct {
	events chan<- string
}

func (h channelLogHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h channelLogHandler) Handle(_ context.Context, record slog.Record) error {
	h.events <- record.Message
	return nil
}

func (h channelLogHandler) WithAttrs([]slog.Attr) slog.Handler { return h }

func (h channelLogHandler) WithGroup(string) slog.Handler { return h }

func waitForLog(t *testing.T, events <-chan string, message string) {
	t.Helper()
	for {
		select {
		case event := <-events:
			if event == message {
				return
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for log %q", message)
		}
	}
}
