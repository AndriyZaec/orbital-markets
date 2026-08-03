package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
)

func TestLiveBalancesDoNotLeakAnotherWalletPair(t *testing.T) {
	server := newAccountScopedBalanceServer(t, map[string]liveAccountSnapshot{
		"pacifica-a": connectedSnapshot("pacifica", "pacifica-a", 46.16, 40),
	}, map[string]liveAccountSnapshot{
		"0xaaa": connectedSnapshot("hyperliquid", "0xaaa", 99.60, 99.60),
	})
	startTestAccountFeeds(t, server.live, "pacifica-a", "0xaaa")

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

func TestLivePreTradeUsesScopedAccountFeeds(t *testing.T) {
	server := newAccountScopedBalanceServer(t, nil, nil)
	server.live.accounts.factories["pacifica"] = &fakeAccountFeedFactory{
		blockers: map[string][]string{"pacifica-a": {"insufficient margin"}},
	}
	server.live.accounts.factories["hyperliquid"] = &fakeAccountFeedFactory{
		blockers: map[string][]string{"0xaaa": {"insufficient margin"}},
	}
	accounts, err := server.live.acquireAccounts("pacifica-a", "0xaaa")
	if err != nil {
		t.Fatal(err)
	}
	defer accounts.Release()
	plan := &domain.ExecutionPlan{
		Leg1: domain.Leg{Venue: "pacifica", Asset: "PENGU", Leverage: 5, MarginRequired: 25},
		Leg2: domain.Leg{Venue: "hyperliquid", Asset: "PENGU", Leverage: 5, MarginRequired: 25},
	}

	blockers := livePreTradeBlockers(plan, accounts)
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

func TestLiveBalancesReturnEachMatchingWalletPair(t *testing.T) {
	server := newAccountScopedBalanceServer(t, map[string]liveAccountSnapshot{
		"pacifica-a": connectedSnapshot("pacifica", "pacifica-a", 46.16, 40),
		"pacifica-b": connectedSnapshot("pacifica", "pacifica-b", 21, 18),
	}, map[string]liveAccountSnapshot{
		"0xaaa": connectedSnapshot("hyperliquid", "0xaaa", 99.60, 99.60),
		"0xbbb": connectedSnapshot("hyperliquid", "0xbbb", 55, 50),
	})
	startTestAccountFeeds(t, server.live, "pacifica-a", "0xaaa")
	startTestAccountFeeds(t, server.live, "pacifica-b", "0xbbb")

	assertBalancePair(t, server, "pacifica-a", "0xAaA", 46.16, 99.60)
	assertBalancePair(t, server, "pacifica-b", "0xBbB", 21, 55)
}

func assertBalancePair(t *testing.T, server *Server, pacifica, hyperliquid string, pacEquity, hlEquity float64) {
	t.Helper()
	request := httptest.NewRequest("GET", "/api/v1/live/balances?account_pacifica="+pacifica+"&account_hyperliquid="+hyperliquid, nil)
	response := httptest.NewRecorder()
	server.handleLiveBalances(response, request)
	var balances map[string]venueAccountStatus
	if err := json.Unmarshal(response.Body.Bytes(), &balances); err != nil {
		t.Fatal(err)
	}
	if got := balances["pacifica"].Equity; got != pacEquity {
		t.Fatalf("Pacifica equity = %v, want %v", got, pacEquity)
	}
	if got := balances["hyperliquid"].Equity; got != hlEquity {
		t.Fatalf("Hyperliquid equity = %v, want %v", got, hlEquity)
	}
}

func newAccountScopedBalanceServer(
	t *testing.T,
	pacifica, hyperliquid map[string]liveAccountSnapshot,
) *Server {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	registry := newAccountFeedRegistry(ctx, map[string]accountFeedFactory{
		"pacifica":    &fakeAccountFeedFactory{snapshots: pacifica},
		"hyperliquid": &fakeAccountFeedFactory{snapshots: hyperliquid},
	}, accountFeedRegistryConfig{})
	return &Server{live: &LiveDeps{accounts: registry}}
}

func startTestAccountFeeds(t *testing.T, live *LiveDeps, pacifica, hyperliquid string) {
	t.Helper()
	accounts, err := live.acquireAccounts(pacifica, hyperliquid)
	if err != nil {
		t.Fatal(err)
	}
	accounts.Release()
}

func connectedSnapshot(venue, account string, equity, available float64) liveAccountSnapshot {
	now := time.Now()
	return liveAccountSnapshot{
		Venue: venue, Account: account, Connected: true,
		LastUpdated: now, PositionsUpdatedAt: now,
		Equity: equity, Available: available,
	}
}
