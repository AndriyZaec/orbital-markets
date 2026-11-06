package orders

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/rpc"
)

func historyOrder(qid string) map[string]any {
	return map[string]any{"traderPdaIndex": 0, "traderSubaccountIndex": 0, "slot": 130, "slotIndex": 2, "instructionIndex": 0, "eventIndex": 1, "marketSymbol": "SOL", "instructionType": "PlaceMarketOrder", "orderType": "market", "createdAt": time.Now().UTC(), "status": "filled", "isReduceOnly": true, "isStopLoss": false,
		"intended":            map[string]any{"clientOrderId": qid, "orderKind": "ImmediateOrCancel", "side": "buy", "priceInTicks": 10000, "baseLots": 29, "lastValidSlot": 164, "orderFlags": 128, "baseQty": "0.29", "hasPriceLimit": true, "isReduceOnly": true, "isStopLoss": false, "isPostOnly": false, "isTakeOnly": true, "cancelExisting": false},
		"fillBeforePlacement": map[string]any{"totalBaseQtyFilled": "0.15", "totalQuoteQtyFilled": "14.95", "totalTakerFee": "0.005", "numTrades": 2, "totalNumFills": 2}, "placedOrder": nil, "currentOrderState": nil}
}
func historyTrades(signature string) []map[string]any {
	return []map[string]any{
		{"traderPdaIndex": 0, "subaccountIndex": 0, "slot": 130, "slotIndex": 2, "instructionIndex": 0, "eventIndex": 2, "marketSymbol": "SOL", "signature": signature, "fillId": "fill-1", "instructionType": "PlaceMarketOrder", "tradeType": "market", "liquidity": "taker", "baseLotsBefore": "-29", "baseLotsAfter": "-19", "baseLotsDelta": "10", "virtualQuoteLotsBefore": "29000000", "virtualQuoteLotsAfter": "19000000", "virtualQuoteLotsDelta": "-10000000", "price": "100"},
		{"traderPdaIndex": 0, "subaccountIndex": 0, "slot": 130, "slotIndex": 2, "instructionIndex": 0, "eventIndex": 3, "marketSymbol": "SOL", "signature": signature, "fillId": "fill-2", "instructionType": "PlaceMarketOrder", "tradeType": "market", "liquidity": "taker", "baseLotsBefore": "-19", "baseLotsAfter": "-14", "baseLotsDelta": "5", "virtualQuoteLotsBefore": "19000000", "virtualQuoteLotsAfter": "14000000", "virtualQuoteLotsDelta": "-5000000", "price": "99"},
	}
}

type historyFixture struct {
	orders, trades atomic.Value
	mode           atomic.Int32
	reads          atomic.Int32
	history        *History
}

func (f *historyFixture) set(t *testing.T, order map[string]any, trades []map[string]any) {
	t.Helper()
	data := []map[string]any{}
	if order != nil {
		data = append(data, order)
	}
	o, err := json.Marshal(map[string]any{"data": data, "hasMore": false})
	if err != nil {
		t.Fatal(err)
	}
	f.orders.Store(o)
	if trades == nil {
		trades = []map[string]any{}
	}
	b, err := json.Marshal(map[string]any{"data": trades, "hasMore": false})
	if err != nil {
		t.Fatal(err)
	}
	f.trades.Store(b)
}
func newHistoryFixture(t *testing.T) *historyFixture {
	t.Helper()
	f := &historyFixture{}
	f.set(t, nil, nil)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.reads.Add(1)
		if r.URL.Query().Get("market_symbol") != "SOL" || r.URL.Query().Get("limit") != "100" || !strings.Contains(r.URL.Path, "/v1/traders/") {
			t.Error("unscoped history read", r.URL)
		}
		if f.mode.Load() == 1 {
			w.Header().Set("Retry-After", "60")
			w.WriteHeader(429)
			return
		}
		if f.mode.Load() == 2 {
			_, _ = w.Write([]byte(`{"data":[],"hasMore":true,"nextCursor":"same-cursor"}`))
			return
		}
		if strings.HasSuffix(r.URL.Path, "/orders_v2") {
			if r.URL.Query().Get("start_time") == "" || r.URL.Query().Get("end_time") == "" || r.URL.Query().Get("organize_twap_orders") != "false" {
				t.Error("order history window/view not bounded")
			}
			_, _ = w.Write(f.orders.Load().([]byte))
		} else {
			if f.mode.Load() == 3 {
				var response map[string]any
				_ = json.Unmarshal(f.trades.Load().([]byte), &response)
				response["hasMore"], response["nextCursor"] = true, "older-wallet-history"
				_ = json.NewEncoder(w).Encode(response)
				return
			}
			_, _ = w.Write(f.trades.Load().([]byte))
		}
	}))
	t.Cleanup(server.Close)
	f.history = NewHistory(server.URL, server.Client())
	return f
}

func TestHistoryCorrelatesTerminalIOCAndDeduplicatesExactFillEvents(t *testing.T) {
	f := newHistoryFixture(t)
	q, _ := requestFixture(t)
	rows := historyTrades("signature")
	rows = append(rows, rows[0])
	f.set(t, historyOrder(q.ClientOrderID), rows)
	fill, err := f.history.Resolve(context.Background(), Attempt{Request: *q, Signature: "signature"}, 130)
	if err != nil || fill.BaseLots != "15" || fill.Status != "partial_fill" || len(fill.FillIDs) != 2 || fill.BaseQuantity != "0.1500000000000000000000000000" || fill.QuoteQuantity != "14.95" {
		t.Fatal(fill, err)
	}
}

func TestHistoryUsesExactTerminalNotionalRatherThanRoundedTradePricesOrClosingBasis(t *testing.T) {
	f := newHistoryFixture(t)
	q, _ := requestFixture(t)
	rows := historyTrades("signature")
	// A history point may summarize multiple matches at a rounded VWAP. Its
	// virtual quote delta is closing-position basis, not executed notional.
	rows[0]["price"] = "99.666666666666666667"
	rows[1]["price"] = "99.666666666666666667"
	f.set(t, historyOrder(q.ClientOrderID), rows)
	fill, err := f.history.Resolve(context.Background(), Attempt{Request: *q, Signature: "signature"}, 130)
	if err != nil || fill.QuoteQuantity != "14.95" || fill.ExchangeFee != "0.005" {
		t.Fatal(fill, err)
	}
}

func TestCompleteRecentOrderDoesNotRequireScanningWalletLifetimeHistory(t *testing.T) {
	f := newHistoryFixture(t)
	q, _ := requestFixture(t)
	f.set(t, historyOrder(q.ClientOrderID), historyTrades("signature"))
	f.mode.Store(3)
	fill, err := f.history.Resolve(context.Background(), Attempt{Request: *q, Signature: "signature"}, 130)
	if err != nil || fill.BaseLots != "15" || f.reads.Load() != 2 {
		t.Fatal("recent complete fill scanned old wallet history", fill, err, f.reads.Load())
	}
}

func TestHistoryNeverInfersFillFromUnrelatedOrIncompleteEvidence(t *testing.T) {
	for _, kind := range []string{"CID", "signature", "subaccount", "opposite", "conflicting duplicate", "missing trade", "missing aggregate", "native limit", "native expiry", "scope"} {
		t.Run(kind, func(t *testing.T) {
			f := newHistoryFixture(t)
			q, _ := requestFixture(t)
			order := historyOrder(q.ClientOrderID)
			rows := historyTrades("signature")
			switch kind {
			case "CID":
				order["intended"].(map[string]any)["clientOrderId"] = "42"
			case "signature":
				rows[0]["signature"] = "other"
			case "subaccount":
				rows[0]["subaccountIndex"] = 1
			case "opposite":
				rows[0]["baseLotsAfter"] = "-39"
				rows[0]["baseLotsDelta"] = "-10"
			case "conflicting duplicate":
				copy := map[string]any{}
				for k, v := range rows[0] {
					copy[k] = v
				}
				copy["eventIndex"] = 4
				rows = append(rows, copy)
			case "missing trade":
				rows = rows[:1]
			case "missing aggregate":
				order["fillBeforePlacement"] = nil
				rows = nil
			case "native limit":
				order["intended"].(map[string]any)["priceInTicks"] = 10001
			case "native expiry":
				order["intended"].(map[string]any)["lastValidSlot"] = 165
			case "scope":
				order["marketSymbol"] = "BTC"
			}
			f.set(t, order, rows)
			if fill, err := f.history.Resolve(context.Background(), Attempt{Request: *q, Signature: "signature"}, 130); err == nil || fill != nil {
				t.Fatal("uncertain history became a fill", fill, err)
			}
		})
	}
}

func TestExplicitZeroFillAndHistoryBackoffBounds(t *testing.T) {
	f := newHistoryFixture(t)
	q, _ := requestFixture(t)
	order := historyOrder(q.ClientOrderID)
	order["status"] = "cancelled"
	order["fillBeforePlacement"] = map[string]any{"totalBaseQtyFilled": "0", "totalQuoteQtyFilled": "0", "totalTakerFee": "0", "numTrades": 0, "totalNumFills": 0}
	f.set(t, order, nil)
	fill, err := f.history.Resolve(context.Background(), Attempt{Request: *q, Signature: "signature"}, 130)
	if err != nil || fill.Status != "no_fill" || fill.BaseLots != "0" {
		t.Fatal(fill, err)
	}
	f.mode.Store(2)
	if _, err := f.history.Resolve(context.Background(), Attempt{Request: *q, Signature: "signature"}, 130); err == nil {
		t.Fatal("cursor loop was accepted")
	}
	f.mode.Store(1)
	before := f.reads.Load()
	_, _ = f.history.Resolve(context.Background(), Attempt{Request: *q, Signature: "signature"}, 130)
	_, _ = f.history.Resolve(context.Background(), Attempt{Request: *q, Signature: "signature"}, 130)
	if f.reads.Load() != before+1 {
		t.Fatal("429 caused repeated history reads")
	}
}

func TestReconcileRequiresFreshAccountThenPersistsPartialFillAcrossServiceRecreation(t *testing.T) {
	f := newHistoryFixture(t)
	s, b, q, signed := testService(t, f.history)
	result, err := s.Submit(context.Background(), q, signed, admit)
	if err != nil {
		t.Fatal(err)
	}
	f.set(t, historyOrder(q.ClientOrderID), historyTrades(result.OrderID))
	b.statusMu.Lock()
	b.status = &rpc.SignatureStatus{Slot: 130, ConfirmationStatus: "confirmed", Err: json.RawMessage("null")}
	b.statusMu.Unlock()
	if _, err := s.Reconcile(context.Background(), q.Account, q.ID, func(context.Context, uint64) error { return errors.New("account stream gap") }); err == nil {
		t.Fatal("stale account confirmed a fill")
	}
	if a, _ := s.store.Get(context.Background(), q.Account, q.ID); a.State != "submitted" {
		t.Fatal(a.State)
	}
	a, err := s.Reconcile(context.Background(), q.Account, q.ID, func(_ context.Context, slot uint64) error {
		if slot != 130 {
			t.Fatal(slot)
		}
		return nil
	})
	if err != nil || a.State != "filled" || a.Fill.BaseLots != "15" {
		t.Fatal(a, err)
	}
	reads := f.reads.Load()
	next := NewService(s.store, b, f.history)
	a, err = next.Reconcile(context.Background(), q.Account, q.ID, nil)
	if err != nil || a.Fill.Status != "partial_fill" || f.reads.Load() != reads || b.sends.Load() != 1 {
		t.Fatal("durable fill lost/re-read/resent", a, err)
	}
}
