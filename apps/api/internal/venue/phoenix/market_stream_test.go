package phoenix

import (
	"context"
	"encoding/json"
	"errors"
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

func TestMarketStreamAppliesExchangeSnapshotAndMarketStats(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/v1/view/exchange":
			writeMetadataFixture(response, "rest-map")
		case "/v1/ws":
			connection, err := upgrader.Upgrade(response, request, nil)
			if err != nil {
				t.Errorf("upgrade websocket: %v", err)
				return
			}
			defer connection.Close()
			assertSubscriptions(t, connection)
			exchangeSnapshot := `{"channel":"exchange","messageType":"snapshot","version":1,"sequenceNumber":"41","exchange":{"perpAssetMap":"ws-map","globalTraderIndex":["ws-index"],"activeTraderBuffer":["ws-buffer"]},"markets":[{"symbol":"SOL","marketStatus":"active","marketPubkey":"ws-market","splinePubkey":"ws-spline","tickSize":100,"baseLotsDecimals":2,"fundingConfig":{"fundingIntervalSeconds":7200},"leverageTiers":[{"maxLeverage":20}]}]}`
			if err := connection.WriteMessage(websocket.TextMessage, []byte(exchangeSnapshot)); err != nil {
				t.Errorf("write exchange snapshot: %v", err)
				return
			}
			assertOrderbookSubscription(t, connection, "subscribe", "SOL")
			for _, message := range []string{
				exchangeSnapshot, // Periodic full snapshots must not reconnect the stream.
				`{"channel":"marketStatsV2","stats":[{"symbol":"SOL","timestamp":1793020205,"openInterest":65520.02,"markPrice":101.33,"midPrice":101.345,"oraclePrice":101.31,"currentFundingRate":0.02}]}`,
				`{"channel":"orderbook","symbol":"SOL","orderbook":{"bids":[[100,2]],"asks":[[102,3]],"mid":101}}`,
			} {
				if err := connection.WriteMessage(websocket.TextMessage, []byte(message)); err != nil {
					t.Errorf("write websocket message: %v", err)
					return
				}
			}
			for {
				if _, _, err := connection.ReadMessage(); err != nil {
					return
				}
			}
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	stream := NewMarketStream(NewClient(server.URL, server.Client()), discardLogger())
	stream.wsURL = "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/ws"
	stream.now = func() time.Time { return time.Unix(1793020210, 0) }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- stream.Run(ctx) }()
	waitForSnapshot(t, stream, func(snapshot MarketStreamSnapshot) bool {
		return snapshot.Synchronized && snapshot.Books["SOL"].BidPrice > 0
	})

	snapshot := stream.Snapshot()
	if snapshot.Sequence != 41 || snapshot.Generation != 1 || !snapshot.Connected {
		t.Fatalf("unexpected stream state: %+v", snapshot)
	}
	if got := snapshot.Metadata.Accounts.PerpAssetMap; got != "ws-map" {
		t.Fatalf("perp asset map = %q", got)
	}
	if len(snapshot.Metadata.Markets) != 1 || snapshot.Metadata.Markets[0].FundingInterval != 2*time.Hour {
		t.Fatalf("unexpected websocket metadata: %+v", snapshot.Metadata.Markets)
	}
	stats := snapshot.Stats["SOL"]
	if stats.MarkPrice != 101.33 || stats.IndexPrice != 101.31 || stats.OpenInterest != 65520.02 ||
		stats.NativeFundingRate != 0.0002 || stats.HourlyFundingRate != 0.0001 {
		t.Fatalf("unexpected market stats: %+v", stats)
	}
	if stats.SourceTime != time.Unix(1793020205, 0).UTC() || stats.ReceivedAt != time.Unix(1793020210, 0).UTC() {
		t.Fatalf("unexpected market stat times: %+v", stats)
	}
	book := snapshot.Books["SOL"]
	if book.BidPrice != 100 || book.BidSizeUSD != 200 || book.AskPrice != 102 || book.AskSizeUSD != 306 {
		t.Fatalf("unexpected BBO: %+v", book)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("run error = %v", err)
	}
}

func TestMarketStreamAppliesOrderedExchangeDeltasAtomically(t *testing.T) {
	stream := NewMarketStream(nil, discardLogger())
	stream.markConnected()
	sequence := stringUint64(7)
	snapshot := exchangeEnvelope{
		MessageType: "snapshot", Version: 1, SequenceNumber: &sequence,
		Exchange: wsExchangeAccounts{PerpAssetMap: "map", GlobalTraderIndex: []string{"index"}, ActiveTraderBuffer: []string{"buffer"}},
		Markets: []wsMarketConfig{{
			Symbol: "SOL", MarketStatus: "active", MarketPubkey: "market", SplinePubkey: "spline",
			TickSize: 100, BaseLotsDecimals: 2, FundingConfig: wsFundingConfig{FundingIntervalSeconds: 3600},
			LeverageTiers: []leverageTier{{MaxLeverage: 20}},
		}},
	}
	if err := stream.applyExchangeMessage(1, snapshot); err != nil {
		t.Fatalf("apply snapshot: %v", err)
	}
	if err := stream.applyMarketStats(1, marketStatsV2Envelope{Stats: []marketStatsV2Wire{{
		Symbol: "SOL", Timestamp: 10, MarkPrice: 100, OraclePrice: 100, CurrentFundingRate: 0.02,
	}}}); err != nil {
		t.Fatal(err)
	}
	ops := []json.RawMessage{
		json.RawMessage(`{"kind":"marketStatusChanged","symbol":"SOL","new_market_status":"paused"}`),
		json.RawMessage(`{"kind":"marketParameterUpdated","symbol":"SOL","update":{"kind":"fundingParametersUpdated","new":{"fundingIntervalSeconds":7200}}}`),
		json.RawMessage(`{"kind":"marketAdded","market":{"symbol":"AAPL","marketStatus":"active","marketPubkey":"aapl-market","splinePubkey":"aapl-spline","tickSize":10,"baseLotsDecimals":-1,"fundingConfig":{"fundingIntervalSeconds":3600},"leverageTiers":[{"maxLeverage":10}]}}`),
	}
	if err := stream.applyExchangeDelta(1, 8, ops); err != nil {
		t.Fatalf("apply delta: %v", err)
	}
	state := stream.Snapshot()
	if state.Sequence != 8 || len(state.Metadata.Markets) != 2 {
		t.Fatalf("unexpected state after delta: %+v", state)
	}
	if state.Metadata.Markets[1].Symbol != "SOL" || state.Metadata.Markets[1].Status != "paused" ||
		state.Metadata.Markets[1].FundingInterval != 2*time.Hour {
		t.Fatalf("unexpected SOL metadata: %+v", state.Metadata.Markets[1])
	}
	if stats := state.Stats["SOL"]; stats.NativeFundingRate != 0.0002 || stats.HourlyFundingRate != 0.0001 {
		t.Fatalf("funding interval delta lost fractional units: %+v", stats)
	}

	badOps := []json.RawMessage{
		json.RawMessage(`{"kind":"marketStatusChanged","symbol":"SOL","new_market_status":"closed"}`),
		json.RawMessage(`{"kind":"marketStatusChanged","symbol":"MISSING","new_market_status":"closed"}`),
	}
	if err := stream.applyExchangeDelta(1, 9, badOps); err == nil {
		t.Fatal("expected invalid delta error")
	}
	state = stream.Snapshot()
	if state.Sequence != 8 || state.Metadata.Markets[1].Status != "paused" {
		t.Fatalf("failed delta mutated state: %+v", state)
	}
}

func TestMarketStreamPreservesProtocolRootAcrossSnapshotsAndKeyUpdates(t *testing.T) {
	stream := NewMarketStream(nil, discardLogger())
	generation := stream.markConnected()
	sequence := stringUint64(7)
	message := exchangeEnvelope{MessageType: "snapshot", Version: 1, SequenceNumber: &sequence,
		Exchange: wsExchangeAccounts{CurrentAuthorities: exchangeAuthorities{RootAuthority: "protocol-root"}, PerpAssetMap: "map", GlobalTraderIndex: []string{"index"}, ActiveTraderBuffer: []string{"buffer"}},
		Markets:  []wsMarketConfig{{Symbol: "SOL", MarketStatus: "active", MarketPubkey: "market", SplinePubkey: "spline", TickSize: 100, BaseLotsDecimals: 2, FundingConfig: wsFundingConfig{FundingIntervalSeconds: 3600}, LeverageTiers: []leverageTier{{MaxLeverage: 20}}}},
	}
	if err := stream.applyExchangeMessage(generation, message); err != nil {
		t.Fatal(err)
	}
	if got := stream.Snapshot().Metadata.Accounts.ProtocolRootAuthority; got != "protocol-root" {
		t.Fatalf("snapshot root lost: %q", got)
	}
	sequence = 8
	message.MessageType = "delta"
	message.Ops = []json.RawMessage{json.RawMessage(`{"kind":"exchangeKeysUpdated","exchange":{"currentAuthorities":{"rootAuthority":"new-protocol-root"},"perpAssetMap":"map","globalTraderIndex":["index"],"activeTraderBuffer":["buffer"]}}`)}
	if err := stream.applyExchangeMessage(generation, message); err != nil {
		t.Fatal(err)
	}
	if got := stream.Snapshot().Metadata.Accounts.ProtocolRootAuthority; got != "new-protocol-root" {
		t.Fatalf("updated root lost: %q", got)
	}
}

func TestMarketStreamAcceptsPeriodicExchangeSnapshotsWithoutLosingLiveState(t *testing.T) {
	stream := NewMarketStream(nil, discardLogger())
	generation := stream.markConnected()
	sequence := stringUint64(7)
	message := exchangeEnvelope{MessageType: "snapshot", Version: 1, SequenceNumber: &sequence,
		Exchange: wsExchangeAccounts{PerpAssetMap: "map", GlobalTraderIndex: []string{"index"}, ActiveTraderBuffer: []string{"buffer"}},
		Markets: []wsMarketConfig{{Symbol: "SOL", MarketStatus: "active", MarketPubkey: "market", SplinePubkey: "spline",
			TickSize: 100, BaseLotsDecimals: 2, FundingConfig: wsFundingConfig{FundingIntervalSeconds: 3600}, LeverageTiers: []leverageTier{{MaxLeverage: 20}}}},
	}
	if err := stream.applyExchangeMessage(generation, message); err != nil {
		t.Fatal(err)
	}
	if err := stream.applyMarketStats(generation, marketStatsV2Envelope{Stats: []marketStatsV2Wire{{Symbol: "SOL", Timestamp: 10, MarkPrice: 100, OraclePrice: 100}}}); err != nil {
		t.Fatal(err)
	}
	stream.mu.Lock()
	stream.books["SOL"] = LiveBook{Symbol: "SOL", BidPrice: 99, AskPrice: 101, ReceivedAt: time.Unix(10, 0), Generation: generation}
	stream.mu.Unlock()
	before := stream.Snapshot()
	for range 3 {
		if err := stream.applyExchangeMessage(generation, message); err != nil {
			t.Fatalf("periodic unchanged snapshot disconnected a healthy stream: %v", err)
		}
	}
	after := stream.Snapshot()
	if !after.Connected || !after.Synchronized || after.Generation != before.Generation || after.Sequence != before.Sequence ||
		after.Stats["SOL"] != before.Stats["SOL"] || after.Books["SOL"] != before.Books["SOL"] {
		t.Fatal("unchanged exchange snapshot invalidated live data or refreshed old prices")
	}
	// A newer full snapshot is authoritative even when it replaces missed deltas.
	sequence = 10
	message.Markets[0].MarketStatus = "paused"
	if err := stream.applyExchangeMessage(generation, message); err != nil {
		t.Fatalf("new full snapshot was rejected: %v", err)
	}
	after = stream.Snapshot()
	if after.Sequence != 10 || after.Generation != generation || !after.Synchronized || after.Metadata.Markets[0].Status != "paused" {
		t.Fatalf("new full snapshot not applied: %+v", after)
	}
	if _, found := after.Books["SOL"]; found {
		t.Fatal("paused market retained an executable book")
	}
	sequence = 9
	if err := stream.applyExchangeMessage(generation, message); err == nil || stream.Snapshot().Sequence != 10 {
		t.Fatal("regressed snapshot was accepted or rolled back live state")
	}
	sequence = 10
	message.Markets[0].MarketStatus = "active"
	if err := stream.applyExchangeMessage(generation, message); err == nil || stream.Snapshot().Metadata.Markets[0].Status != "paused" {
		t.Fatal("conflicting snapshot with unchanged sequence was accepted")
	}
}

func TestMarketStreamRejectsStatsBatchAtomically(t *testing.T) {
	stream := NewMarketStream(nil, discardLogger())
	stream.markConnected()
	stream.mu.Lock()
	stream.haveExchangeSnapshot = true
	stream.markets["SOL"] = MarketMetadata{Symbol: "SOL", FundingInterval: time.Hour}
	stream.markets["BTC"] = MarketMetadata{Symbol: "BTC", FundingInterval: time.Hour}
	stream.stats["SOL"] = LiveMarketStats{Symbol: "SOL", MarkPrice: 100}
	stream.mu.Unlock()

	err := stream.applyMarketStats(1, marketStatsV2Envelope{Stats: []marketStatsV2Wire{
		{Symbol: "SOL", Timestamp: 10, OpenInterest: 1, MarkPrice: 101, OraclePrice: 101},
		{Symbol: "BTC", Timestamp: 10, OpenInterest: 1, MarkPrice: 0, OraclePrice: 101},
	}})
	if err == nil {
		t.Fatal("expected invalid stats error")
	}
	if got := stream.Snapshot().Stats["SOL"].MarkPrice; got != 100 {
		t.Fatalf("rejected batch changed SOL mark to %v", got)
	}
}

func TestMarketStreamClearsBBOWhenBookSideBecomesEmpty(t *testing.T) {
	stream := NewMarketStream(nil, discardLogger())
	generation := stream.markConnected()
	stream.mu.Lock()
	stream.markets["SOL"] = MarketMetadata{Symbol: "SOL", Status: "active"}
	stream.books["SOL"] = LiveBook{Symbol: "SOL", BidPrice: 100, AskPrice: 101}
	stream.mu.Unlock()

	if err := stream.applyOrderbook(generation, orderbookEnvelope{
		Symbol: "SOL", Orderbook: orderbookWire{Asks: [][]float64{{101, 2}}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, ok := stream.Snapshot().Books["SOL"]; ok {
		t.Fatal("empty bid side retained stale BBO")
	}
}

func TestMarketStreamNotifiesWhenDynamicMarketBecomesPublishable(t *testing.T) {
	stream := NewMarketStream(nil, discardLogger())
	generation := stream.markConnected()
	stream.mu.Lock()
	stream.haveExchangeSnapshot = true
	stream.synchronized = true
	stream.markets["SOL"] = MarketMetadata{Symbol: "SOL", Status: "active", FundingInterval: time.Hour}
	stream.books["SOL"] = LiveBook{Symbol: "SOL", BidPrice: 100, AskPrice: 101}
	stream.mu.Unlock()

	if err := stream.applyMarketStats(generation, marketStatsV2Envelope{Stats: []marketStatsV2Wire{{
		Symbol: "SOL", Timestamp: 10, OpenInterest: 1, MarkPrice: 100, OraclePrice: 100,
	}}}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stream.Updates():
	default:
		t.Fatal("publishable market did not notify scanner")
	}
}

func TestMarketStreamRejectsExchangeSequenceGap(t *testing.T) {
	stream := NewMarketStream(nil, discardLogger())
	stream.markConnected()
	stream.mu.Lock()
	stream.haveExchangeSnapshot = true
	stream.sequence = 12
	stream.mu.Unlock()

	err := stream.applyExchangeDelta(1, 14, nil)
	if !errors.Is(err, errExchangeSequenceGap) {
		t.Fatalf("error = %v, want sequence gap", err)
	}
	if got := stream.Snapshot().Sequence; got != 12 {
		t.Fatalf("sequence = %d", got)
	}
}

func TestMarketStreamReloadsRESTMetadataBeforeReconnect(t *testing.T) {
	var metadataRequests atomic.Int32
	var websocketConnections atomic.Int32
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/v1/view/exchange":
			metadataRequests.Add(1)
			writeMetadataFixture(response, "rest-map")
		case "/v1/ws":
			connection, err := upgrader.Upgrade(response, request, nil)
			if err != nil {
				t.Errorf("upgrade websocket: %v", err)
				return
			}
			assertSubscriptions(t, connection)
			websocketConnections.Add(1)
			_ = connection.Close()
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	stream := NewMarketStream(NewClient(server.URL, server.Client()), discardLogger())
	stream.wsURL = "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/ws"
	stream.reconnectWait = time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- stream.Run(ctx) }()
	deadline := time.Now().Add(2 * time.Second)
	for metadataRequests.Load() < 2 || websocketConnections.Load() < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("metadata requests = %d, websocket connections = %d", metadataRequests.Load(), websocketConnections.Load())
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("run error = %v", err)
	}
}

func assertSubscriptions(t *testing.T, connection *websocket.Conn) {
	t.Helper()
	if err := connection.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set subscription read deadline: %v", err)
	}
	channels := make(map[string]map[string]any)
	for range 2 {
		var request struct {
			Type         string         `json:"type"`
			Subscription map[string]any `json:"subscription"`
		}
		if err := connection.ReadJSON(&request); err != nil {
			t.Fatalf("read subscription: %v", err)
		}
		if request.Type != "subscribe" {
			t.Fatalf("subscription type = %q", request.Type)
		}
		channel, _ := request.Subscription["channel"].(string)
		channels[channel] = request.Subscription
	}
	if encoding := channels["exchange"]["encoding"]; encoding != "json" {
		t.Fatalf("exchange encoding = %v", encoding)
	}
	if _, ok := channels["marketStatsV2"]; !ok {
		t.Fatal("missing marketStatsV2 subscription")
	}
}

func assertOrderbookSubscription(t *testing.T, connection *websocket.Conn, action, symbol string) {
	t.Helper()
	var request struct {
		Type         string `json:"type"`
		Subscription struct {
			Channel string `json:"channel"`
			Symbol  string `json:"symbol"`
		} `json:"subscription"`
	}
	if err := connection.ReadJSON(&request); err != nil {
		t.Fatalf("read orderbook subscription: %v", err)
	}
	if request.Type != action || request.Subscription.Channel != "orderbook" || request.Subscription.Symbol != symbol {
		t.Fatalf("unexpected orderbook subscription: %+v", request)
	}
}

func writeMetadataFixture(response http.ResponseWriter, perpAssetMap string) {
	response.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(response, `{"keys":{"perpAssetMap":"`+perpAssetMap+`","globalTraderIndex":["rest-index"],"activeTraderBuffer":["rest-buffer"]},"markets":[{"symbol":"SOL","marketStatus":"active","marketPubkey":"rest-market","splinePubkey":"rest-spline","tickSize":100,"baseLotsDecimals":2,"fundingIntervalSeconds":3600,"leverageTiers":[{"maxLeverage":25}]}]}`)
}

func waitForSnapshot(t *testing.T, stream *MarketStream, ready func(MarketStreamSnapshot) bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if ready(stream.Snapshot()) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for market stream state")
		}
		time.Sleep(time.Millisecond)
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
