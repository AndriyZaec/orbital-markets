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
		if r.URL.Path != "/fapi/v3/order" || r.URL.Query().Get("symbol") != "2ZUSDT" ||
			r.URL.Query().Get("origClientOrderId") != "orbital-order-1" || r.Method != http.MethodGet {
			t.Fatalf("request = %s %s", r.Method, r.URL.String())
		}
		fmt.Fprint(w, `{"orderId":548991212,"clientOrderId":"orbital-order-1","symbol":"2ZUSDT","status":"FILLED","executedQty":"315","avgPrice":"0.04747"}`)
	}))
	defer venue.Close()
	reader := NewService(store, NewClient(venue.URL, venue.Client(), func() time.Time { return *now }), nil, func() time.Time { return *now })

	order, err := reader.LookupOrder(context.Background(), testOwner, "2ZUSDT", "orbital-order-1")
	if err != nil {
		t.Fatal(err)
	}
	if order.OrderID != "548991212" || order.ClientOrderID != "orbital-order-1" || order.Symbol != "2ZUSDT" ||
		order.Status != "FILLED" || order.ExecutedQuantity != 315 || order.AveragePrice != 0.04747 {
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

func TestReaderPaginatesFundingWithinBounds(t *testing.T) {
	store, now := approvedReaderService(t)
	since := now.Add(-2 * time.Hour)
	until := *now
	pages := 0
	venue := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages++
		query := r.URL.Query()
		if r.URL.Path != "/fapi/v3/income" || r.Method != http.MethodGet || query.Get("incomeType") != "FUNDING_FEE" ||
			query.Get("startTime") != fmt.Sprint(since.UnixMilli()) || query.Get("endTime") != fmt.Sprint(until.UnixMilli()) ||
			query.Get("limit") != "100" || query.Get("page") != fmt.Sprint(pages) {
			t.Fatalf("request = %s %s", r.Method, r.URL.String())
		}
		count := 100
		if pages == 2 {
			count = 1
		}
		rows := make([]map[string]any, 0, count)
		for index := range count {
			id := (pages-1)*100 + index + 1
			rows = append(rows, map[string]any{
				"symbol": "2ZUSDT", "incomeType": "FUNDING_FEE", "income": "-0.01", "asset": "USDT",
				"time": since.Add(time.Duration(id) * time.Millisecond).UnixMilli(), "tranId": fmt.Sprint(id),
			})
		}
		if err := json.NewEncoder(w).Encode(rows); err != nil {
			t.Fatal(err)
		}
	}))
	defer venue.Close()
	reader := NewService(store, NewClient(venue.URL, venue.Client(), func() time.Time { return *now }), nil, func() time.Time { return *now })

	payments, err := reader.ReadFunding(context.Background(), testOwner, since, until)
	if err != nil {
		t.Fatal(err)
	}
	if pages != 2 || len(payments) != 101 || payments[0].ExternalID != "1" || payments[100].ExternalID != "101" ||
		payments[0].Account != testOwner || payments[0].Asset != "2Z" || payments[0].MarketKey != "2ZUSDT" {
		t.Fatalf("pages = %d, payments = %+v", pages, payments)
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

func TestReaderRejectsOversizedFundingPage(t *testing.T) {
	store, now := approvedReaderService(t)
	venue := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		rows := make([]map[string]any, 101)
		for index := range rows {
			rows[index] = map[string]any{
				"symbol": "2ZUSDT", "incomeType": "FUNDING_FEE", "income": "1", "asset": "USDT",
				"time": now.Add(-time.Minute).UnixMilli(), "tranId": fmt.Sprint(index + 1),
			}
		}
		_ = json.NewEncoder(w).Encode(rows)
	}))
	defer venue.Close()
	reader := NewService(store, NewClient(venue.URL, venue.Client(), func() time.Time { return *now }), nil, func() time.Time { return *now })

	if _, err := reader.ReadFunding(context.Background(), testOwner, now.Add(-time.Hour), *now); err == nil {
		t.Fatal("oversized funding page was accepted")
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
