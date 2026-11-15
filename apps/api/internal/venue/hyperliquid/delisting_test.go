package hyperliquid

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestInactiveMetadataStopsQuotesAndPreservesUniverseIndices(t *testing.T) {
	for _, flags := range []string{
		`"isDelisted":true`,
		`"onlyIsolated":true`,
		`"marginMode":"strictIsolated"`,
		`"marginMode":"noCross"`,
		`"onlyIsolated":true,"marginMode":"strictIsolated"`,
	} {
		t.Run(flags, func(t *testing.T) {
			testInactiveMetadataStopsQuotesAndPreservesUniverseIndices(t, flags)
		})
	}
}

func testInactiveMetadataStopsQuotesAndPreservesUniverseIndices(t *testing.T, flags string) {
	t.Helper()
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
	body = fmt.Sprintf(`[{"universe":[{"name":"OLD",%s,"szDecimals":2},{"name":"BTC","szDecimals":3}]},
		[{"markPx":"10","oraclePx":"10","funding":"0.0001","openInterest":"100"},
		 {"markPx":"100","oraclePx":"100","funding":"0.0001","openInterest":"100"}]]`, flags)
	adapter.pollREST(context.Background())
	generation := adapter.markStreamConnected()
	adapter.applyBBO(generation, bboData{Coin: "OLD", BBO: []bboLevel{{Px: "9", Sz: "1"}, {Px: "11", Sz: "1"}}})
	snapshots, err := adapter.FetchMarketData(context.Background())
	if err != nil || len(snapshots) != 1 || snapshots[0].Asset != "BTC" {
		t.Fatalf("quotes after inactive metadata = %+v, err=%v", snapshots, err)
	}
	if index, ok := adapter.AssetMap().AssetIndex("BTC"); !ok || index != 1 {
		t.Fatalf("BTC universe index = %d, %t; want 1", index, ok)
	}
	if index, ok := adapter.AssetMap().AssetIndex("OLD"); !ok || index != 0 {
		t.Fatalf("inactive market universe index = %d, %t; want 0", index, ok)
	}
	if decimals, ok := adapter.AssetMap().SizeDecimals("OLD"); !ok || decimals != 2 {
		t.Fatalf("inactive market size decimals = %d, %t; want 2", decimals, ok)
	}
	if assets := adapter.InactiveAssets(); len(assets) != 1 || assets[0] != "OLD" {
		t.Fatalf("inactive assets = %v", assets)
	}
	fail = true
	adapter.pollREST(context.Background())
	if len(adapter.InactiveAssets()) != 1 {
		t.Fatal("failed request forgot inactive metadata")
	}
	fail = false
	body = `[{"universe":[{"name":"BTC","szDecimals":3}]},
		[{"markPx":"100","oraclePx":"100","funding":"0.0001","openInterest":"100"}]]`
	adapter.pollREST(context.Background())
	if len(adapter.InactiveAssets()) != 1 {
		t.Fatal("omitted metadata forgot inactive market")
	}
	body = `[{"universe":[{"name":"OLD","szDecimals":2}]},
		[{"markPx":"10","oraclePx":"10","funding":"0.0001","openInterest":"100"}]]`
	adapter.pollREST(context.Background())
	if len(adapter.InactiveAssets()) != 0 || adapter.assets["OLD"] == nil {
		t.Fatal("explicitly active market was not restored")
	}
}

func TestInactiveMetadataWithoutAssetContextStillRemovesMarket(t *testing.T) {
	for _, flags := range []string{
		`"isDelisted":true`,
		`"onlyIsolated":true`,
		`"marginMode":"strictIsolated"`,
		`"marginMode":"noCross"`,
	} {
		t.Run(flags, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = fmt.Fprintf(w, `[{"universe":[{"name":"OLD",%s}]},[]]`, flags)
			}))
			defer server.Close()
			adapter := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
			adapter.marketURL = server.URL
			adapter.assets["OLD"] = &assetState{markPrice: 10}
			adapter.pollREST(context.Background())
			if len(adapter.assets) != 0 || len(adapter.InactiveAssets()) != 1 {
				t.Fatal("explicit inactive metadata was ignored without quote context")
			}
		})
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
