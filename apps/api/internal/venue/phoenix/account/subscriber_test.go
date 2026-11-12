package account

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

type waitingContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (c *waitingContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

func streamSnapshot(t *testing.T, sequence, slot uint64) []byte {
	t.Helper()
	m := snapshotMessage(t)
	m.Channel = "traderState"
	*m.Subaccounts[0].Sequence, *m.Slot = sequence, slot
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func await(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for !condition() {
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatal("timed out waiting for account state")
		}
	}
}

func TestSubscriberGapReconnectAndCancellation(t *testing.T) {
	rest := fixture(t, "trader-snapshot")
	first := streamSnapshot(t, 41, 100)
	second := streamSnapshot(t, 44, 104)
	gap := deltaMessage(t)
	*gap.Deltas[0].Sequence, *gap.Slot = 43, 103
	gapRaw, err := json.Marshal(gap)
	if err != nil {
		t.Fatal(err)
	}
	var connections atomic.Int32
	var reads atomic.Int32
	releaseGap := make(chan struct{})
	releaseSecond := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ws" {
			reads.Add(1)
			if r.URL.Path != "/v1/trader/state/"+owner || r.URL.Query().Get("traderPdaIndex") != "0" {
				t.Error("wrong cross-trader REST request")
			}
			_, _ = w.Write(rest)
			return
		}
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		var subscription struct {
			Type         string `json:"type"`
			Subscription struct {
				Channel   string `json:"channel"`
				Authority string `json:"authority"`
				Index     int    `json:"traderPdaIndex"`
			} `json:"subscription"`
		}
		if err := conn.ReadJSON(&subscription); err != nil {
			return
		}
		if subscription.Type != "subscribe" || subscription.Subscription.Channel != "traderState" || subscription.Subscription.Authority != owner || subscription.Subscription.Index != 0 {
			t.Error("wrong trader subscription")
		}
		if connections.Add(1) == 1 {
			_ = conn.WriteMessage(websocket.TextMessage, first)
			select {
			case <-releaseGap:
			case <-ctx.Done():
				return
			}
			_ = conn.WriteMessage(websocket.TextMessage, gapRaw)
		} else {
			select {
			case <-releaseSecond:
			case <-ctx.Done():
				return
			}
			_ = conn.WriteMessage(websocket.TextMessage, second)
		}
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer server.Close()
	state, _ := NewState(owner)
	subscriber := NewSubscriber(state, NewReader(server.URL, server.Client()), quietLogger())
	subscriber.wsURL = "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"
	subscriber.reconnectWait = time.Millisecond
	finished := make(chan error, 1)
	go func() { finished <- subscriber.Run(ctx) }()
	await(t, func() bool { return state.Snapshot().Fresh(time.Now()) })
	close(releaseGap)
	await(t, func() bool { return connections.Load() == 2 })
	if snapshot := state.Snapshot(); snapshot.Synchronized || !snapshot.PositionsUpdatedAt.IsZero() {
		t.Fatal("reconnect was ready before its snapshot")
	}
	close(releaseSecond)
	await(t, func() bool { return state.Snapshot().Sequence == 44 && state.Snapshot().Fresh(time.Now()) })
	if reads.Load() < 2 {
		t.Fatal("reconnect did not refetch REST")
	}
	select {
	case <-subscriber.Updates():
	default:
		t.Fatal("missing coalesced lifecycle notification")
	}
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("subscriber did not stop")
	}
	if snapshot := state.Snapshot(); snapshot.Connected || snapshot.Synchronized || !snapshot.PositionsUpdatedAt.IsZero() {
		t.Fatal("shutdown left authoritative account state")
	}
}

func TestRefreshSingleFlightAndIndependentWaiterCancellation(t *testing.T) {
	raw := fixture(t, "trader-snapshot")
	entered := make(chan struct{})
	release := make(chan struct{})
	var reads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if reads.Add(1) == 1 {
			close(entered)
		}
		<-release
		_, _ = w.Write(raw)
	}))
	defer server.Close()
	state, _ := NewState(owner)
	subscriber := NewSubscriber(state, NewReader(server.URL, server.Client()), quietLogger())
	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() { first <- subscriber.Refresh(ctx) }()
	<-entered
	cancel()
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	// The original bounded request stays alive for another waiter.
	second := make(chan error, 1)
	secondContext := &waitingContext{Context: context.Background(), waiting: make(chan struct{})}
	go func() { second <- subscriber.Refresh(secondContext) }()
	<-secondContext.waiting
	close(release)
	if err := <-second; err != nil {
		t.Fatal(err)
	}
	if reads.Load() != 1 || state.Snapshot().Sequence != 41 {
		t.Fatal("refresh did not share the request")
	}
}

func TestRESTBackoffStatusAndSizeBounds(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusUnauthorized, http.StatusNotFound} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var reads atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reads.Add(1)
				w.Header().Set("Retry-After", "60")
				w.WriteHeader(status)
			}))
			defer server.Close()
			reader := NewReader(server.URL, server.Client())
			if _, _, err := reader.read(context.Background(), owner); err == nil {
				t.Fatal("accepted failed REST response")
			}
			if status == http.StatusTooManyRequests {
				if _, _, err := reader.read(context.Background(), owner); err == nil {
					t.Fatal("ignored REST backoff")
				}
				if reads.Load() != 1 || time.Until(reader.retryAt) < 55*time.Second {
					t.Fatal("ignored Retry-After")
				}
			}
		})
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat(" ", maxMessageBytes+1))
	}))
	defer server.Close()
	if _, _, err := NewReader(server.URL, server.Client()).read(context.Background(), owner); err == nil {
		t.Fatal("accepted oversized account response")
	}
}

func TestRefreshCanceledOrMalformedReadDoesNotRenewAccount(t *testing.T) {
	s, _, at := readyState(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "not-json") }))
	defer server.Close()
	subscriber := NewSubscriber(s, NewReader(server.URL, server.Client()), quietLogger())
	if err := subscriber.Refresh(context.Background()); err == nil {
		t.Fatal("accepted malformed read")
	}
	if !s.Snapshot().ObservedAt.Equal(at) {
		t.Fatal("failed read renewed freshness")
	}
}
