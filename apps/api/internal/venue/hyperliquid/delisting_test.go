package hyperliquid

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestDelistingStopsQuotesAndPreservesUniverseIndices(t *testing.T) {
	body := `[{"universe":[{"name":"OLD","szDecimals":2},{"name":"BTC","szDecimals":3}]},
		[{"markPx":"10","oraclePx":"10","funding":"0.0001","openInterest":"100"},
		 {"markPx":"100","oraclePx":"100","funding":"0.0001","openInterest":"100"}]]`
	fail := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if fail {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, body)
	}))
	defer server.Close()
	adapter := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	adapter.marketURL = server.URL
	adapter.pollREST(context.Background())
	if len(adapter.assets) != 2 {
		t.Fatal("initial active markets were not loaded")
	}
	body = `[{"universe":[{"name":"OLD","isDelisted":true,"szDecimals":2},{"name":"BTC","szDecimals":3}]},
		[{"markPx":"10","oraclePx":"10","funding":"0.0001","openInterest":"100"},
		 {"markPx":"100","oraclePx":"100","funding":"0.0001","openInterest":"100"}]]`
	adapter.pollREST(context.Background())
	generation := adapter.markStreamConnected()
	adapter.applyBBO(generation, bboData{Coin: "OLD", BBO: []bboLevel{{Px: "9", Sz: "1"}, {Px: "11", Sz: "1"}}})
	snapshots, err := adapter.FetchMarketData(context.Background())
	if err != nil || len(snapshots) != 1 || snapshots[0].Asset != "BTC" {
		t.Fatalf("quotes after delisting = %+v, err=%v", snapshots, err)
	}
	if index, ok := adapter.AssetMap().AssetIndex("BTC"); !ok || index != 1 {
		t.Fatalf("BTC universe index = %d, %t; want 1", index, ok)
	}
	if assets := adapter.InactiveAssets(); len(assets) != 1 || assets[0] != "OLD" {
		t.Fatalf("inactive assets = %v", assets)
	}
	fail = true
	adapter.pollREST(context.Background())
	if len(adapter.InactiveAssets()) != 1 {
		t.Fatal("failed request forgot delisting")
	}
	fail = false
	body = `[{"universe":[{"name":"BTC","szDecimals":3}]},
		[{"markPx":"100","oraclePx":"100","funding":"0.0001","openInterest":"100"}]]`
	adapter.pollREST(context.Background())
	if len(adapter.InactiveAssets()) != 1 {
		t.Fatal("omitted metadata forgot delisting")
	}
	body = `[{"universe":[{"name":"OLD","szDecimals":2}]},
		[{"markPx":"10","oraclePx":"10","funding":"0.0001","openInterest":"100"}]]`
	adapter.pollREST(context.Background())
	if len(adapter.InactiveAssets()) != 0 || adapter.assets["OLD"] == nil {
		t.Fatal("explicitly relisted market was not restored")
	}
}

func TestDelistingWithoutAssetContextStillRemovesMarket(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `[{"universe":[{"name":"OLD","isDelisted":true}]},[]]`)
	}))
	defer server.Close()
	adapter := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	adapter.marketURL = server.URL
	adapter.assets["OLD"] = &assetState{markPrice: 10}
	adapter.pollREST(context.Background())
	if len(adapter.assets) != 0 || len(adapter.InactiveAssets()) != 1 {
		t.Fatal("explicit delisting was ignored without quote context")
	}
}

func TestBBOSubscriptionsFollowActiveMarkets(t *testing.T) {
	messages := make(chan wsSubscription, 3)
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			var message wsSubscription
			if err := conn.ReadJSON(&message); err != nil {
				return
			}
			messages <- message
		}
	}))
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	adapter := New(slog.Default())
	adapter.assets["OLD"] = &assetState{}
	subscribed := make(map[string]bool)
	for _, method := range []string{"subscribe", "unsubscribe", "subscribe"} {
		if method == "unsubscribe" {
			delete(adapter.assets, "OLD")
		} else {
			adapter.assets["OLD"] = &assetState{}
		}
		if err := adapter.reconcileBBOSubscriptions(conn, subscribed); err != nil {
			t.Fatal(err)
		}
		select {
		case message := <-messages:
			if message.Method != method {
				t.Fatalf("subscription method = %s, want %s", message.Method, method)
			}
		case <-time.After(time.Second):
			t.Fatalf("no %s message received", method)
		}
	}
}
