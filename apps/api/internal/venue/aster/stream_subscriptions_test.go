package aster

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type subscriptionEvent struct {
	Connection int
	Query      string
	Method     string   `json:"method"`
	Params     []string `json:"params"`
	ID         uint64   `json:"id"`
	At         time.Time
	Close      func() `json:"-"`
}

func subscriptionServer(t *testing.T) (*httptest.Server, <-chan subscriptionEvent) {
	t.Helper()
	events := make(chan subscriptionEvent, 32)
	var connections atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		connection := int(connections.Add(1))
		events <- subscriptionEvent{Connection: connection, Query: r.URL.Query().Get("streams"), Close: func() { _ = conn.Close() }}
		for {
			var event subscriptionEvent
			if err := conn.ReadJSON(&event); err != nil {
				return
			}
			event.Connection, event.At = connection, time.Now()
			events <- event
			if err := conn.WriteJSON(map[string]any{"result": nil, "id": event.ID}); err != nil {
				return
			}
			if event.Method == "SUBSCRIBE" {
				for _, stream := range event.Params {
					symbol := strings.ToUpper(strings.TrimSuffix(stream, "@bookTicker"))
					frame := strings.Replace(string(filterBookFrame(symbol)), `"!bookTicker"`, fmt.Sprintf("%q", stream), 1)
					if err := conn.WriteMessage(websocket.TextMessage, []byte(frame)); err != nil {
						return
					}
				}
			}
		}
	}))
	t.Cleanup(server.Close)
	return server, events
}

func nextSubscriptionEvent(t *testing.T, events <-chan subscriptionEvent) subscriptionEvent {
	t.Helper()
	select {
	case event := <-events:
		return event
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for subscription event")
		return subscriptionEvent{}
	}
}

func startSubscriptionConnection(t *testing.T, a *Adapter) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.connectAndListen(ctx) }()
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("connection did not stop")
		}
	}
	t.Cleanup(stop)
	return stop
}

func TestBookSubscriptionsTransitionGrowAndReconnect(t *testing.T) {
	rest := newMutableMarketServer(t, &mutableMarketServerState{})
	defer rest.Close()
	ws, events := subscriptionServer(t)
	a := newTestAdapter(rest.URL)
	a.now = func() time.Time { return mutableMarketNow }
	a.wsURL = "ws" + strings.TrimPrefix(ws.URL, "http") + "/stream?streams=!markPrice@arr@1s/!bookTicker"
	if err := a.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	stop := startSubscriptionConnection(t, a)
	initial := nextSubscriptionEvent(t, events)
	if initial.Query != "!markPrice@arr@1s/!bookTicker" {
		t.Fatalf("before allow-list query=%q", initial.Query)
	}
	select {
	case event := <-events:
		t.Fatalf("subscription changed before first allow-list: %+v", event)
	case <-time.After(30 * time.Millisecond):
	}
	a.SetCrossVenueAssets([]string{"BTC"})
	unsub := nextSubscriptionEvent(t, events)
	if unsub.Method != "UNSUBSCRIBE" || !reflect.DeepEqual(unsub.Params, []string{"!bookTicker"}) {
		t.Fatalf("all-market transition=%+v", unsub)
	}
	sub := nextSubscriptionEvent(t, events)
	if sub.Method != "SUBSCRIBE" || !reflect.DeepEqual(sub.Params, []string{"btcusdt@bookTicker"}) {
		t.Fatalf("initial subscriptions=%+v", sub)
	}
	a.SetCrossVenueAssets([]string{"ETH"})
	grown := nextSubscriptionEvent(t, events)
	if grown.Connection != initial.Connection || grown.Method != "SUBSCRIBE" || !reflect.DeepEqual(grown.Params, []string{"ethusdt@bookTicker"}) {
		t.Fatalf("growth reconnected or subscribed incorrect symbols: %+v", grown)
	}
	// The per-symbol envelope uses the same update and freshness rules.
	deadline := time.Now().Add(time.Second)
	for {
		a.mu.RLock()
		state, generation := a.markets["ETHUSDT"], a.streamGeneration
		a.mu.RUnlock()
		if state.bookUpdateID == 20 {
			if generation != 1 || !state.bookUpdatedAt.Equal(time.UnixMilli(1000)) {
				t.Fatalf("subscription changed generation or book timestamp: %+v", state)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("per-symbol book was not applied")
		}
		time.Sleep(time.Millisecond)
	}
	stop()
	startSubscriptionConnection(t, a)
	reconnected := nextSubscriptionEvent(t, events)
	if reconnected.Connection == initial.Connection || reconnected.Query != "!markPrice@arr@1s" {
		t.Fatalf("reconnect did not start with mark-only URL: %+v", reconnected)
	}
	restored := nextSubscriptionEvent(t, events)
	if restored.Method != "SUBSCRIBE" || !reflect.DeepEqual(restored.Params, []string{"btcusdt@bookTicker", "ethusdt@bookTicker"}) {
		t.Fatalf("reconnect did not restore lifetime union: %+v", restored)
	}
}

func TestBookSubscriptionBatchesAndConnectionLimit(t *testing.T) {
	ws, events := subscriptionServer(t)
	rest, _ := newMarketServer(t)
	defer rest.Close()
	a := newTestAdapter(rest.URL)
	a.wsURL = "ws" + strings.TrimPrefix(ws.URL, "http") + "/stream?streams=!markPrice@arr@1s/!bookTicker"
	const books = 400 // ceil((400 books + 1 mark) / 200) = 3 connections.
	assets := make([]string, books)
	for i := range assets {
		asset := fmt.Sprintf("ASSET%d", i)
		assets[i] = asset
		a.markets[asset+"USDT"] = marketState{marketMetadata: marketMetadata{asset: asset}}
	}
	a.SetCrossVenueAssets(assets)
	stop := startSubscriptionConnection(t, a)
	counts := make(map[int]int)
	var shardClose func()
	count := 0
	var previous time.Time
	for count < books {
		event := nextSubscriptionEvent(t, events)
		if event.Query != "" {
			if event.Connection == 1 {
				if event.Query != "!markPrice@arr@1s" {
					t.Fatalf("primary streams=%s", event.Query)
				}
				counts[event.Connection] = 1
			} else {
				initial := strings.Split(event.Query, "/")
				if len(initial) > bookSubscribeBatch {
					t.Fatal("shard URL batch too large")
				}
				counts[event.Connection] = len(initial)
				count += len(initial)
				shardClose = event.Close
			}
			continue
		}
		if event.Method != "SUBSCRIBE" || len(event.Params) > bookSubscribeBatch || len(event.Params) == 0 {
			t.Fatalf("invalid subscription batch: %+v", event)
		}
		if !previous.IsZero() && event.At.Sub(previous) < subscribeCommandPace-20*time.Millisecond {
			t.Fatal("subscription commands exceeded local rate bound")
		}
		previous = event.At
		count += len(event.Params)
		counts[event.Connection] += len(event.Params)
		if counts[event.Connection] > maxStreamsPerConnection {
			t.Fatalf("connection exceeded stream limit: %v", counts)
		}
	}
	if count != books || len(counts) != (books+1+maxStreamsPerConnection-1)/maxStreamsPerConnection {
		t.Fatalf("subscriptions=%d connections=%v", count, counts)
	}
	a.mu.Lock()
	a.markets["EXTRAUSDT"] = marketState{marketMetadata: marketMetadata{asset: "EXTRA"}}
	a.mu.Unlock()
	a.SetCrossVenueAssets([]string{"EXTRA"})
	growth := nextSubscriptionEvent(t, events)
	if growth.Connection != 3 || growth.Method != "SUBSCRIBE" || !reflect.DeepEqual(growth.Params, []string{"extrausdt@bookTicker"}) {
		t.Fatalf("did not use free shard slots: %+v", growth)
	}
	shardClose()
	deadline := time.Now().Add(3 * time.Second)
	for {
		a.mu.RLock()
		connected, synchronized, generation := a.streamConnected, a.streamSynchronized, a.streamGeneration
		a.mu.RUnlock()
		if !connected && !synchronized {
			if generation != 1 {
				t.Fatal("shard creation changed generation")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("failed shard did not disconnect the shared generation")
		}
		time.Sleep(time.Millisecond)
	}
	stop()
	// A failure on a secondary shard reconnects the entire cohort, restoring
	// the complete subscription union without all-market bookTicker.
	startSubscriptionConnection(t, a)
	restoredBooks := 0
	restoredConnections := make(map[int]struct{})
	for restoredBooks < books+1 {
		event := nextSubscriptionEvent(t, events)
		if event.Query != "" {
			restoredConnections[event.Connection] = struct{}{}
			if event.Query == "!markPrice@arr@1s" {
				continue
			}
			if strings.Contains(event.Query, "!") {
				t.Fatalf("reconnect used an all-market book stream: %+v", event)
			}
			restoredBooks += len(strings.Split(event.Query, "/"))
		} else {
			restoredBooks += len(event.Params)
		}
	}
	if len(restoredConnections) != 3 || restoredBooks != books+1 {
		t.Fatalf("restored %d books on %d connections", restoredBooks, len(restoredConnections))
	}
}
