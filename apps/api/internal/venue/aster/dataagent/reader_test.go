package dataagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestReaderReturnsOneCompleteAccountObservation(t *testing.T) {
	store, now := approvedReaderService(t)
	requests := make([]string, 0, 4)
	venue := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.Path)
		switch r.URL.Path {
		case "/fapi/v3/positionSide/dual":
			fmt.Fprint(w, `{"dualSidePosition":false}`)
		case "/fapi/v3/accountWithJoinMargin":
			fmt.Fprint(w, `{"canTrade":true,"totalMarginBalance":"123.5","availableBalance":"100.25","positions":[]}`)
		case "/fapi/v3/positionRisk":
			fmt.Fprint(w, `[{"symbol":"2ZUSDT","positionSide":"BOTH","positionAmt":"315","entryPrice":"0.04747","unRealizedProfit":"2","leverage":"5","liquidationPrice":"0.03","isolatedMargin":"10"}]`)
		case "/fapi/v3/leverageBracket":
			if r.URL.Query().Get("symbol") != "" {
				t.Fatal("all leverage brackets must be requested without a symbol")
			}
			fmt.Fprint(w, `[{"symbol":"2ZUSDT","brackets":[{"initialLeverage":5,"notionalCap":100000,"notionalFloor":0}]}]`)
		default:
			http.Error(w, "unexpected", http.StatusNotFound)
		}
	}))
	defer venue.Close()
	clockCalls := 0
	client := NewClient(venue.URL, venue.Client(), func() time.Time {
		value := now.Add(time.Duration(clockCalls) * time.Second)
		clockCalls++
		return value
	})
	reader := NewService(store, client, nil, func() time.Time { return *now })
	status, err := store.StatusByOwner(context.Background(), testOwner)
	if err != nil {
		t.Fatal(err)
	}

	observation, err := reader.ReadAccount(context.Background(), testOwner)
	if err != nil {
		t.Fatal(err)
	}
	if observation.DataAgent != status.AgentAddress || !observation.PositionMode.OneWay || observation.Margin.Equity != 123.5 || len(observation.Positions) != 1 ||
		observation.Positions[0].Symbol != "2ZUSDT" || len(observation.LeverageBrackets["2ZUSDT"]) != 1 ||
		!observation.ObservedAt.Equal(*now) {
		t.Fatalf("observation = %+v", observation)
	}
	want := []string{"/fapi/v3/positionSide/dual", "/fapi/v3/accountWithJoinMargin", "/fapi/v3/positionRisk", "/fapi/v3/leverageBracket"}
	if fmt.Sprint(requests) != fmt.Sprint(want) {
		t.Fatalf("requests = %v, want %v", requests, want)
	}
}

func TestReaderReturnsNoPartialAccountObservation(t *testing.T) {
	store, now := approvedReaderService(t)
	venue := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/fapi/v3/positionSide/dual":
			fmt.Fprint(w, `{"dualSidePosition":false}`)
		case "/fapi/v3/accountWithJoinMargin":
			fmt.Fprint(w, `{"canTrade":true,"totalMarginBalance":"123.5","availableBalance":"100.25","positions":[]}`)
		case "/fapi/v3/positionRisk":
			fmt.Fprint(w, `{}`)
		default:
			t.Fatal("partial account read continued after failure")
		}
	}))
	defer venue.Close()
	reader := NewService(store, NewClient(venue.URL, venue.Client(), func() time.Time { return *now }), nil, func() time.Time { return *now })

	observation, err := reader.ReadAccount(context.Background(), testOwner)
	if err == nil || !observation.ObservedAt.IsZero() || observation.Margin.Equity != 0 || len(observation.Positions) != 0 {
		t.Fatalf("observation = %+v, error = %v", observation, err)
	}
}

func TestReaderReadsExactSymbolLeverageBrackets(t *testing.T) {
	store, now := approvedReaderService(t)
	venue := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/fapi/v3/leverageBracket" || r.URL.Query().Get("symbol") != "PIPPINUSDT" {
			t.Fatalf("request = %s", r.URL.String())
		}
		fmt.Fprint(w, `{"symbol":"PIPPINUSDT","brackets":[{"initialLeverage":20,"notionalCap":10000,"notionalFloor":0}]}`)
	}))
	defer venue.Close()
	reader := NewService(store, NewClient(venue.URL, venue.Client(), func() time.Time { return *now }), nil, func() time.Time { return *now })

	brackets, observedAt, err := reader.ReadLeverageBrackets(context.Background(), testOwner, "PIPPINUSDT")
	if err != nil {
		t.Fatal(err)
	}
	if len(brackets["PIPPINUSDT"]) != 1 || brackets["PIPPINUSDT"][0].InitialLeverage != 20 || !observedAt.Equal(*now) {
		t.Fatalf("brackets = %+v at %s", brackets, observedAt)
	}
}

func TestReaderReportsUnreadableCredentialWithoutCallingAster(t *testing.T) {
	store, now := approvedReaderService(t)
	wrongKeyStore, err := NewStore(store.db, bytes.Repeat([]byte{0x7f}, 32))
	if err != nil {
		t.Fatal(err)
	}
	called := false
	venue := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	defer venue.Close()
	reader := NewService(wrongKeyStore, NewClient(venue.URL, venue.Client(), func() time.Time { return *now }), nil, func() time.Time { return *now })

	if _, err := reader.ReadAccount(context.Background(), testOwner); !errors.Is(err, ErrCredentialUnreadable) {
		t.Fatalf("read error = %v", err)
	}
	if called {
		t.Fatal("Aster was called with an unreadable credential")
	}
}

func TestReaderPreservesAsterReadRejection(t *testing.T) {
	store, now := approvedReaderService(t)
	venue := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"code":-2015,"msg":"rejected"}`)
	}))
	defer venue.Close()
	reader := NewService(store, NewClient(venue.URL, venue.Client(), func() time.Time { return *now }), nil, func() time.Time { return *now })

	if _, err := reader.ReadAccount(context.Background(), testOwner); !errors.Is(err, ErrReadRejected) {
		t.Fatalf("read error = %v", err)
	}
}

func TestReaderRejectsAccountWithoutPositionLeverageBrackets(t *testing.T) {
	store, now := approvedReaderService(t)
	venue := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/fapi/v3/positionSide/dual":
			fmt.Fprint(w, `{"dualSidePosition":false}`)
		case "/fapi/v3/accountWithJoinMargin":
			fmt.Fprint(w, `{"canTrade":true,"totalMarginBalance":"123.5","availableBalance":"100.25","positions":[]}`)
		case "/fapi/v3/positionRisk":
			fmt.Fprint(w, `[{"symbol":"2ZUSDT","positionSide":"BOTH","positionAmt":"315","entryPrice":"0.04747","leverage":"5","liquidationPrice":"0.03"}]`)
		case "/fapi/v3/leverageBracket":
			fmt.Fprint(w, `[{"symbol":"BTCUSDT","brackets":[{"initialLeverage":5,"notionalCap":100000,"notionalFloor":0}]}]`)
		}
	}))
	defer venue.Close()
	reader := NewService(store, NewClient(venue.URL, venue.Client(), func() time.Time { return *now }), nil, func() time.Time { return *now })

	if _, err := reader.ReadAccount(context.Background(), testOwner); err == nil {
		t.Fatal("account without position leverage brackets was accepted")
	}
}

func TestReaderLooksUpOneCorrelatedOrder(t *testing.T) {
	store, now := approvedReaderService(t)
	venue := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/fapi/v3/order":
			if r.URL.Query().Get("symbol") != "2ZUSDT" ||
				r.URL.Query().Get("origClientOrderId") != "orbital-order-1" || r.Method != http.MethodGet {
				t.Fatalf("request = %s %s", r.Method, r.URL.String())
			}
			fmt.Fprint(w, `{"orderId":548991212,"clientOrderId":"orbital-order-1","symbol":"2ZUSDT","status":"FILLED","executedQty":"315","avgPrice":"0.04747"}`)
		case "/fapi/v3/userTrades":
			if r.URL.Query().Get("symbol") != "2ZUSDT" || r.URL.Query().Get("orderId") != "548991212" ||
				r.URL.Query().Get("limit") != "1000" || r.Method != http.MethodGet {
				t.Fatalf("request = %s %s", r.Method, r.URL.String())
			}
			fmt.Fprint(w, `[{"orderId":548991212,"symbol":"2ZUSDT","qty":"200","price":"0.04747","commission":"-0.004","commissionAsset":"USDT"},{"orderId":548991212,"symbol":"2ZUSDT","qty":"115","price":"0.04747","commission":"0.003","commissionAsset":"USDT"}]`)
		default:
			t.Fatalf("request = %s %s", r.Method, r.URL.String())
		}
	}))
	defer venue.Close()
	reader := NewService(store, NewClient(venue.URL, venue.Client(), func() time.Time { return *now }), nil, func() time.Time { return *now })

	order, err := reader.LookupOrder(context.Background(), testOwner, "2ZUSDT", "orbital-order-1")
	if err != nil {
		t.Fatal(err)
	}
	if order.OrderID != "548991212" || order.ClientOrderID != "orbital-order-1" || order.Symbol != "2ZUSDT" ||
		order.Status != "FILLED" || order.ExecutedQuantity != 315 || order.AveragePrice != 0.04747 || order.Fee != 0.007 {
		t.Fatalf("order = %+v", order)
	}
}

func TestOrderLookupPreservesAsterReadRejection(t *testing.T) {
	store, now := approvedReaderService(t)
	venue := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"code":-2013,"msg":"Order does not exist"}`)
	}))
	defer venue.Close()
	reader := NewService(store, NewClient(venue.URL, venue.Client(), func() time.Time { return *now }), nil, func() time.Time { return *now })

	if _, err := reader.LookupOrder(context.Background(), testOwner, "2ZUSDT", "orbital-order-1"); !errors.Is(err, ErrReadRejected) {
		t.Fatalf("lookup error = %v", err)
	}
}

func TestReaderSplitsSaturatedFundingRangesAndMergesSorted(t *testing.T) {
	store, now := approvedReaderService(t)
	since := now.Add(-2 * time.Hour)
	until := *now
	mid := time.UnixMilli(since.UnixMilli() + (until.UnixMilli()-since.UnixMilli())/2).UTC()
	type requestRange struct{ start, end int64 }
	requests := make([]requestRange, 0, 3)
	venue := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		if r.URL.Path != "/fapi/v3/income" || r.Method != http.MethodGet || query.Get("incomeType") != "FUNDING_FEE" ||
			query.Get("limit") != "1000" || query.Has("page") {
			t.Fatalf("request = %s %s", r.Method, r.URL.String())
		}
		startMillis, _ := json.Number(query.Get("startTime")).Int64()
		endMillis, _ := json.Number(query.Get("endTime")).Int64()
		requests = append(requests, requestRange{startMillis, endMillis})
		if len(requests) == 1 {
			rows := make([]map[string]any, 1000)
			for index := range rows {
				rows[index] = fundingRow(fmt.Sprint(index+1000), since.Add(time.Duration(index)*time.Millisecond))
			}
			_ = json.NewEncoder(w).Encode(rows)
			return
		}
		if startMillis == since.UnixMilli() && endMillis == mid.UnixMilli() {
			_ = json.NewEncoder(w).Encode([]map[string]any{fundingRow("2", mid)})
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{fundingRow("1", mid.Add(time.Millisecond))})
	}))
	defer venue.Close()
	reader := NewService(store, NewClient(venue.URL, venue.Client(), func() time.Time { return *now }), nil, func() time.Time { return *now })

	payments, err := reader.ReadFunding(context.Background(), testOwner, since, until)
	if err != nil {
		t.Fatal(err)
	}
	wantRequests := []requestRange{{since.UnixMilli(), until.UnixMilli()}, {since.UnixMilli(), mid.UnixMilli()}, {mid.Add(time.Millisecond).UnixMilli(), until.UnixMilli()}}
	if fmt.Sprint(requests) != fmt.Sprint(wantRequests) || len(payments) != 2 || payments[0].ExternalID != "2" || payments[1].ExternalID != "1" ||
		payments[0].Account != testOwner || payments[0].Asset != "2Z" || payments[0].MarketKey != "2ZUSDT" {
		t.Fatalf("requests = %v, payments = %+v", requests, payments)
	}
}

func TestReaderIncludesFundingAtWindowEnd(t *testing.T) {
	store, now := approvedReaderService(t)
	since := now.Add(-incomeWindow - time.Millisecond)
	until := *now
	requests := 0
	venue := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		if requests == 1 {
			fmt.Fprint(w, `[]`)
			return
		}
		fmt.Fprintf(w, `[{"symbol":"2ZUSDT","incomeType":"FUNDING_FEE","income":"1","asset":"USDT","time":%d,"tranId":"1"}]`, until.UnixMilli())
	}))
	defer venue.Close()
	reader := NewService(store, NewClient(venue.URL, venue.Client(), func() time.Time { return *now }), nil, func() time.Time { return *now })

	payments, err := reader.ReadFunding(context.Background(), testOwner, since, until)
	if err != nil || requests != 2 || len(payments) != 1 || !payments[0].PaidAt.Equal(until) {
		t.Fatalf("requests = %d, payments = %+v, error = %v", requests, payments, err)
	}
}

func TestReaderRejectsInvalidFundingRangeResponses(t *testing.T) {
	store, now := approvedReaderService(t)
	tests := []struct {
		name string
		rows []map[string]any
	}{
		{"out of window", []map[string]any{fundingRow("1", now.Add(time.Millisecond))}},
		{"out of order", []map[string]any{fundingRow("1", now.Add(-time.Minute)), fundingRow("2", now.Add(-2*time.Minute))}},
		{"duplicate IDs", []map[string]any{fundingRow("1", now.Add(-2*time.Minute)), fundingRow("1", now.Add(-time.Minute))}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			venue := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(test.rows)
			}))
			defer venue.Close()
			reader := NewService(store, NewClient(venue.URL, venue.Client(), func() time.Time { return *now }), nil, func() time.Time { return *now })
			if _, err := reader.ReadFunding(context.Background(), testOwner, now.Add(-time.Hour), *now); err == nil {
				t.Fatal("invalid funding response was accepted")
			}
		})
	}
}

func TestReaderRejectsSaturatedSingleMillisecondRange(t *testing.T) {
	_, now := approvedReaderService(t)
	venue := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		rows := make([]map[string]any, 1000)
		for index := range rows {
			rows[index] = fundingRow(fmt.Sprint(index+1), *now)
		}
		_ = json.NewEncoder(w).Encode(rows)
	}))
	defer venue.Close()
	client := NewClient(venue.URL, venue.Client(), func() time.Time { return *now })
	if _, err := client.readFunding(context.Background(), testOwner, testDataAgent, bytes.Repeat([]byte{1}, 32), *now, *now); err == nil {
		t.Fatal("saturated single-millisecond range was accepted")
	}
}

func TestReaderBoundsTotalFundingRequests(t *testing.T) {
	store, now := approvedReaderService(t)
	requests := 0
	venue := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		fmt.Fprint(w, `[]`)
	}))
	defer venue.Close()
	reader := NewService(store, NewClient(venue.URL, venue.Client(), func() time.Time { return *now }), nil, func() time.Time { return *now })
	if _, err := reader.ReadFunding(context.Background(), testOwner, now.Add(-701*24*time.Hour), *now); err == nil {
		t.Fatal("funding request budget was not enforced")
	}
	if requests != 100 {
		t.Fatalf("requests = %d, want 100", requests)
	}
}

func fundingRow(id string, paidAt time.Time) map[string]any {
	return map[string]any{
		"symbol": "2ZUSDT", "incomeType": "FUNDING_FEE", "income": "-0.01", "asset": "USDT",
		"time": paidAt.UnixMilli(), "tranId": id,
	}
}

func approvedReaderService(t *testing.T) (*Store, *time.Time) {
	t.Helper()
	service, store, now := newLifecycleService(t)
	prepared := prepareProbe(t, service)
	if err := service.Authorize(context.Background(), prepared.ProbeID, testSignature, testOwner, testExecutionAgent, prepared.Approval); err != nil {
		t.Fatal(err)
	}
	return store, now
}
