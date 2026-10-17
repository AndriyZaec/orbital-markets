package aster

import (
	"context"
	"testing"
)

func TestBookFastPathPreservesEnvelopeAndValidation(t *testing.T) {
	for _, test := range []struct {
		name, body          string
		wantError, wantBook bool
	}{
		{"reordered envelope", `{"data":{"s":"BIOU","u":20,"T":1000,"b":"0.99","B":"10","a":"1.01","A":"10"},"stream":"!bookTicker"}`, false, true},
		{"bad shape", `{"stream":"!bookTicker","data":[]}`, true, false},
		{"wrong wire type", `{"stream":"!bookTicker","data":{"s":"BIOU","b":0.99}}`, true, false},
		{"invalid number", `{"stream":"!bookTicker","data":{"s":"BIOU","T":1000,"b":"NaN","B":"10","a":"1.01","A":"10"}}`, false, false},
		{"unknown stream", `{"stream":"other","data":{"b":0.99}}`, false, false},
		{"truncated", `{"stream":"!bookTicker","data":`, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			a := newTestAdapter("http://unused")
			a.markets["BIOU"] = marketState{}
			err := a.applyStreamMessage([]byte(test.body))
			if (err != nil) != test.wantError {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := a.markets["BIOU"].bidPrice; (got == 0.99) != test.wantBook {
				t.Fatalf("book price=%g, want update=%t", got, test.wantBook)
			}
		})
	}
}

func TestStreamHealthTransitionsNotifyMarketDataConsumers(t *testing.T) {
	server, _ := newMarketServer(t)
	defer server.Close()
	a := newTestAdapter(server.URL)
	a.streamGeneration, a.streamConnected, a.streamSynchronized = 1, true, true
	a.disconnectStreamGeneration(1)
	select {
	case <-a.MarketDataUpdates():
	default:
		t.Fatal("disconnect did not notify scanner")
	}
	a.streamConnected = true
	if err := a.refresh(context.Background(), refreshToken{expectedGeneration: 1, bookGeneration: 1, synchronize: true}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-a.MarketDataUpdates():
	default:
		t.Fatal("successful resync did not notify scanner")
	}
}
