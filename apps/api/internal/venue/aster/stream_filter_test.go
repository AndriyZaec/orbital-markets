package aster

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"
)

func filterBookFrame(symbol string) []byte {
	return []byte(fmt.Sprintf(`{"stream":"!bookTicker","data":{"s":%q,"u":20,"T":1000,"b":"99","B":"2","a":"101","A":"3"}}`, symbol))
}

func TestStreamAssetFilter(t *testing.T) {
	for _, test := range []struct {
		name    string
		lists   [][]string
		updated bool
	}{
		{"before_first_list", nil, true},
		{"excluded", [][]string{{"BTC"}}, false},
		{"empty_first_list", [][]string{{}}, false},
		{"added_later", [][]string{{"BTC"}, {" local "}}, true},
		{"list_cannot_shrink", [][]string{{"LOCAL"}, {"BTC"}, {}}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			a := newTestAdapter("http://unused")
			a.streamConnected, a.streamSynchronized, a.streamGeneration = true, true, 7
			a.now = func() time.Time { return time.Unix(42, 0) }
			before := marketState{marketMetadata: marketMetadata{asset: "LOCAL"}, bidPrice: 98}
			a.markets["LOCALUSDT"] = before
			for _, list := range test.lists {
				a.SetCrossVenueAssets(list)
			}
			if err := a.applyStreamMessage(filterBookFrame("LOCALUSDT")); err != nil {
				t.Fatal(err)
			}
			if test.updated {
				if state := a.markets["LOCALUSDT"]; state.bidPrice != 99 || state.bookGeneration != 7 || state.bookUpdateID != 20 || state.bidSize != 198 {
					t.Fatalf("allowed book not applied: %+v", state)
				}
				if !a.lastStreamReceivedAt.Equal(a.now()) {
					t.Fatal("allowed frame did not renew stream receipt")
				}
			} else if a.markets["LOCALUSDT"] != before || !a.lastStreamReceivedAt.IsZero() || len(a.pendingBooks) != 0 || len(a.unknownStreamSymbols) != 0 {
				t.Fatal("excluded frame changed market, stream receipt, or pending/unknown state")
			}
			if !a.streamConnected || !a.streamSynchronized || a.streamGeneration != 7 {
				t.Fatal("filter changed stream lifecycle")
			}
		})
	}
}

func TestExcludedBookDoesNotTakeMarketLock(t *testing.T) {
	a := newTestAdapter("http://unused")
	a.markets["LOCALUSDT"] = marketState{marketMetadata: marketMetadata{asset: "LOCAL"}}
	a.SetCrossVenueAssets([]string{"BTC"})
	// A wrong wire type would fail JSON decoding if the frame reached it.
	frame := []byte(`{"stream":"!bookTicker","data":{"s":"LOCALUSDT","b":99}}`)
	a.mu.Lock()
	defer a.mu.Unlock()
	done := make(chan error, 1)
	go func() { done <- a.applyStreamMessage(frame) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("excluded book waited for the market lock")
	}
}

func TestStreamAssetAddedAfterDiscardStartsUpdating(t *testing.T) {
	a := newTestAdapter("http://unused")
	a.streamConnected = true
	a.markets["LOCALUSDT"] = marketState{marketMetadata: marketMetadata{asset: "LOCAL"}}
	a.SetCrossVenueAssets([]string{"BTC"})
	frame := filterBookFrame("LOCALUSDT")
	if err := a.applyStreamMessage(frame); err != nil || a.markets["LOCALUSDT"].bidPrice != 0 {
		t.Fatalf("excluded frame was applied: %v", err)
	}
	a.SetCrossVenueAssets([]string{" local "})
	if err := a.applyStreamMessage(frame); err != nil || a.markets["LOCALUSDT"].bidPrice != 99 || a.lastStreamReceivedAt.IsZero() {
		t.Fatalf("newly allowed asset did not start updating: %v", err)
	}
}

func TestStreamFilterPreservesUnknownSymbolsAndRefreshesKnownSymbols(t *testing.T) {
	server, _ := newMarketServer(t)
	defer server.Close()
	a := newTestAdapter(server.URL)
	a.streamConnected = true
	a.SetCrossVenueAssets([]string{"ETH"})
	if err := a.applyStreamMessage(filterBookFrame("BTCUSDT")); err != nil {
		t.Fatal(err)
	}
	if a.pendingBooks["BTCUSDT"].bidPrice != 99 || a.lastStreamReceivedAt.IsZero() {
		t.Fatal("unknown symbol no longer takes the existing pending/observation path")
	}
	if err := a.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	before, received := a.markets["BTCUSDT"], a.lastStreamReceivedAt
	if err := a.applyStreamMessage([]byte(`{"stream":"!bookTicker","data":{"s":"BTCUSDT","u":21,"T":2000,"b":"98","B":"2","a":"102","A":"3"}}`)); err != nil {
		t.Fatal(err)
	}
	if a.markets["BTCUSDT"] != before || a.lastStreamReceivedAt != received {
		t.Fatal("newly known symbol outside the allow-list was not filtered after metadata refresh")
	}
}

func TestMarkArrayFastPathMatchesExistingPath(t *testing.T) {
	data := `[{"e":"markPriceUpdate","E":1000,"s":"BTCUSDT","p":"100","i":"101","r":"0.0008"},{"e":"markPriceUpdate","E":1000,"s":"LOCALUSDT","p":"2","i":"2.1","r":"-0.001"}]`
	var results []map[string]marketState
	for _, frame := range []string{
		`{"stream":"!markPrice@arr@1s","data":` + data + `}`,
		`{"data":` + data + `,"stream":"!markPrice@arr@1s"}`,
	} {
		a := newTestAdapter("http://unused")
		a.streamConnected = true
		a.now = func() time.Time { return time.Unix(42, 0) }
		a.markets["BTCUSDT"] = marketState{marketMetadata: marketMetadata{asset: "BTC"}}
		a.markets["LOCALUSDT"] = marketState{marketMetadata: marketMetadata{asset: "LOCAL"}}
		a.SetCrossVenueAssets([]string{"BTC"})
		if err := a.applyStreamMessage([]byte(frame)); err != nil {
			t.Fatal(err)
		}
		if a.markets["BTCUSDT"].markPrice != 100 || a.markets["LOCALUSDT"].markPrice != 2 || a.lastStreamReceivedAt != a.now() {
			t.Fatal("mark array did not update all markets and stream receipt")
		}
		results = append(results, a.markets)
	}
	if !reflect.DeepEqual(results[0], results[1]) {
		t.Fatal("direct mark array decode changed update semantics")
	}
	for _, data := range []string{`[`, `{}`, `[{"p":100}]`} {
		a := newTestAdapter("http://unused")
		if err := a.applyStreamMessage([]byte(`{"stream":"!markPrice@arr@1s","data":` + data + `}`)); err == nil || !a.lastStreamReceivedAt.IsZero() {
			t.Fatal("malformed mark array accepted or observed")
		}
	}
}

func TestStreamFilterConcurrentPublication(t *testing.T) {
	a := newTestAdapter("http://unused")
	a.streamConnected = true
	a.markets["BTCUSDT"] = marketState{marketMetadata: marketMetadata{asset: "BTC"}}
	a.SetCrossVenueAssets(nil)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			a.SetCrossVenueAssets([]string{fmt.Sprintf("ASSET%d", i), "BTC"})
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			if err := a.applyStreamMessage(filterBookFrame("BTCUSDT")); err != nil {
				t.Error(err)
			}
		}
	}()
	wg.Wait()
	if err := a.applyStreamMessage(filterBookFrame("BTCUSDT")); err != nil || a.markets["BTCUSDT"].bidPrice != 99 {
		t.Fatalf("published allow-list did not enable book updates: %v", err)
	}
}
