package account

import (
	"context"
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
	if info.Equity != 46.159179 {
		t.Fatalf("Equity = %v, want 46.159179", info.Equity)
	}
	if info.AvailableToSpend != 46.159179 {
		t.Fatalf("AvailableToSpend = %v, want 46.159179", info.AvailableToSpend)
	}
	if info.AvailableToWithdraw != 46.159179 {
		t.Fatalf("AvailableToWithdraw = %v, want 46.159179", info.AvailableToWithdraw)
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
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	})}

	if err := subscriber.refreshAccountInfo(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !positionsRequested {
		t.Fatal("positions snapshot was not requested")
	}
	if state.Snapshot().PositionsUpdatedAt.IsZero() {
		t.Fatal("empty positions snapshot did not mark position state ready")
	}
}
