package api

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	hlaccount "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/hyperliquid/account"
	pacaccount "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/pacifica/account"
)

func TestLiveBalancesDoNotLeakAnotherWalletPair(t *testing.T) {
	pacState := pacaccount.NewAccountState()
	pacState.ResetForAccount("pacifica-a")
	pacState.UpdateEquityForAccount("pacifica-a", 46.16, 40, 40, 0, 0)
	pacState.SetConnectedForAccount("pacifica-a", true)

	hlState := hlaccount.NewAccountState()
	hlState.ResetForAccount("0xaaa")
	hlState.UpdateMarginForAccount("0xaaa", hlaccount.MarginSummary{
		AccountEquity:    99.60,
		AvailableBalance: 99.60,
	})
	hlState.SetConnectedForAccount("0xaaa", true)

	server := &Server{live: &LiveDeps{
		pacState:           pacState,
		hlState:            hlState,
		accountCancel:      func() {},
		pacificaAccount:    "pacifica-a",
		hyperliquidAccount: "0xaaa",
	}}
	request := httptest.NewRequest("GET", "/api/v1/live/balances?account_pacifica=pacifica-b&account_hyperliquid=0xbbb", nil)
	response := httptest.NewRecorder()

	server.handleLiveBalances(response, request)
	var balances map[string]venueAccountStatus
	if err := json.Unmarshal(response.Body.Bytes(), &balances); err != nil {
		t.Fatal(err)
	}
	for venue, balance := range balances {
		if balance.Connected || balance.Equity != 0 || balance.Available != 0 {
			t.Fatalf("%s leaked another account balance: %+v", venue, balance)
		}
	}
}

func TestLivePreTradeBlocksZeroBalances(t *testing.T) {
	pacState := pacaccount.NewAccountState()
	pacState.ResetForAccount("pacifica-a")
	pacState.UpdateEquityForAccount("pacifica-a", 0, 0, 0, 0, 0)
	pacState.SetConnectedForAccount("pacifica-a", true)

	hlState := hlaccount.NewAccountState()
	hlState.ResetForAccount("0xaaa")
	hlState.UpdateMarginForAccount("0xaaa", hlaccount.MarginSummary{})
	hlState.SetConnectedForAccount("0xaaa", true)

	server := &Server{live: &LiveDeps{pacState: pacState, hlState: hlState}}
	plan := &domain.ExecutionPlan{
		Leg1: domain.Leg{Venue: "pacifica", Asset: "PENGU", Leverage: 5, MarginRequired: 25},
		Leg2: domain.Leg{Venue: "hyperliquid", Asset: "PENGU", Leverage: 5, MarginRequired: 25},
	}

	blockers := server.livePreTradeBlockers(plan)
	insufficientMargin := 0
	for _, blocker := range blockers {
		if strings.Contains(blocker, "insufficient margin") {
			insufficientMargin++
		}
	}
	if insufficientMargin != 2 {
		t.Fatalf("blockers = %v, want one insufficient-margin blocker per venue", blockers)
	}
}

func TestLiveBalancesReturnMatchingWalletPair(t *testing.T) {
	pacState := pacaccount.NewAccountState()
	pacState.ResetForAccount("pacifica-a")
	pacState.UpdateEquityForAccount("pacifica-a", 46.16, 40, 40, 0, 0)
	pacState.SetConnectedForAccount("pacifica-a", true)

	hlState := hlaccount.NewAccountState()
	hlState.ResetForAccount("0xaaa")
	hlState.UpdateMarginForAccount("0xaaa", hlaccount.MarginSummary{
		AccountEquity:    99.60,
		AvailableBalance: 99.60,
	})
	hlState.SetConnectedForAccount("0xaaa", true)

	server := &Server{live: &LiveDeps{
		pacState:           pacState,
		hlState:            hlState,
		accountCancel:      func() {},
		pacificaAccount:    "pacifica-a",
		hyperliquidAccount: "0xaaa",
	}}
	request := httptest.NewRequest("GET", "/api/v1/live/balances?account_pacifica=pacifica-a&account_hyperliquid=0xAaA", nil)
	response := httptest.NewRecorder()

	server.handleLiveBalances(response, request)
	var balances map[string]venueAccountStatus
	if err := json.Unmarshal(response.Body.Bytes(), &balances); err != nil {
		t.Fatal(err)
	}
	if got := balances["pacifica"].Equity; got != 46.16 {
		t.Fatalf("Pacifica equity = %v, want 46.16", got)
	}
	if got := balances["hyperliquid"].Equity; got != 99.60 {
		t.Fatalf("Hyperliquid equity = %v, want 99.60", got)
	}
}
