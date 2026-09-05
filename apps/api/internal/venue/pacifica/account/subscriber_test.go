package account

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestParseRESTAccountInfo(t *testing.T) {
	raw := []byte(`{
		"success": true,
		"data": {
			"balance": "46.159179",
			"account_equity": "46.159179",
			"available_to_spend": "46.159179",
			"available_to_withdraw": "46.159179",
			"total_margin_used": "0",
			"cross_mmr": "0",
			"updated_at": 1784712476141
		},
		"error": null,
		"code": null
	}`)

	info, err := parseRESTAccountInfo(raw)
	if err != nil {
		t.Fatalf("parseRESTAccountInfo() error = %v", err)
	}
	if info.Equity != 46.159179 || info.AvailableToSpend != 46.159179 || info.AvailableToWithdraw != 46.159179 {
		t.Fatalf("account info = %+v", info)
	}
}

func TestRefreshAccountInfoBootstrapsEmptyPositions(t *testing.T) {
	state := NewAccountState()
	state.ResetForAccount("wallet")
	positionsRequested := false
	subscriber := NewSubscriber(slog.New(slog.NewTextHandler(io.Discard, nil)), state, "wallet", nil)
	subscriber.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		var body string
		switch req.URL.Path {
		case "/api/v1/account":
			body = `{"success":true,"data":{"account_equity":"46.16","available_to_spend":"46.16","available_to_withdraw":"46.16","total_margin_used":"0","cross_mmr":"0"}}`
		case "/api/v1/positions":
			positionsRequested = true
			body = `{"success":true,"data":[]}`
		default:
			t.Fatalf("unexpected request path %q", req.URL.Path)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}

	if err := subscriber.refreshAccountInfo(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !positionsRequested || state.Snapshot().PositionsUpdatedAt.IsZero() {
		t.Fatal("empty positions snapshot did not mark position state ready")
	}
}

func TestParseRESTPositions(t *testing.T) {
	raw := []byte(`{
		"success": true,
		"data": [
			{"symbol":"SOL","side":"bid","amount":"1.25","entry_price":"185.50","margin":"20","liquidation_price":"140.25"},
			{"symbol":"BTC","side":"ask","amount":"0.01","entry_price":"105000","margin":"50","liquidation_price":null},
			{"symbol":"ETH","side":"bid","amount":"0","entry_price":"3000","margin":"0","liquidation_price":null}
		]
	}`)
	positions, err := parseRESTPositions(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(positions) != 2 {
		t.Fatalf("positions = %d, want 2", len(positions))
	}
	if got := positions[0]; got.Symbol != "SOL" || got.Side != "long" || got.Size != 1.25 || got.EntryPrice != 185.50 || got.MarginUsed != 20 || got.LiqPrice != 140.25 {
		t.Fatalf("long position = %+v", got)
	}
	if got := positions[1]; got.Symbol != "BTC" || got.Side != "short" || got.Size != 0.01 || got.EntryPrice != 105000 || got.MarginUsed != 50 || got.LiqPrice != 0 {
		t.Fatalf("short position = %+v", got)
	}
}

func TestParseRESTPositionsRejectsMissingData(t *testing.T) {
	if _, err := parseRESTPositions([]byte(`{"success":true,"data":null}`)); err == nil {
		t.Fatal("expected missing positions data error")
	}
}

func TestRESTAccountParsersRejectMalformedAndNonFiniteNumbers(t *testing.T) {
	if _, err := parseRESTPositions([]byte(`{"success":true,"data":[{"symbol":"BTC","side":"bid","amount":"wat","entry_price":"1","margin":"1"}]}`)); err == nil {
		t.Fatal("expected malformed position amount error")
	}
	if _, err := parseRESTAccountInfo([]byte(`{"success":true,"data":{"account_equity":"NaN","available_to_spend":"1","available_to_withdraw":"1","total_margin_used":"0","cross_mmr":"0"}}`)); err == nil {
		t.Fatal("expected non-finite account equity error")
	}
}

func TestHandleLeverageAcceptsNumberAndString(t *testing.T) {
	for name, payload := range map[string]string{
		"number": `{"s":"VIRTUAL","l":2}`,
		"string": `{"s":"VIRTUAL","l":"2"}`,
	} {
		t.Run(name, func(t *testing.T) {
			state := NewAccountState()
			state.ResetForAccount("owner")
			subscriber := &Subscriber{
				logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
				state:   state,
				account: "owner",
			}
			subscriber.handleLeverage(json.RawMessage(payload))
			if got := state.Snapshot().SymbolConfigs["VIRTUAL"].Leverage; got != 2 {
				t.Fatalf("leverage = %v, want 2", got)
			}
		})
	}
}
