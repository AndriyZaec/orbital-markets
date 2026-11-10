package api

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type recoveringAccountFactory struct {
	fakeAccountFeedFactory
	unavailable atomic.Bool
	attempts    atomic.Int32
}

func (f *recoveringAccountFactory) Start(ctx context.Context, account string) (liveAccountFeed, error) {
	f.attempts.Add(1)
	if f.unavailable.Load() {
		return nil, errors.New("temporary venue outage")
	}
	return f.fakeAccountFeedFactory.Start(ctx, account)
}

func TestAccountViewsIsolateVenueStartupFailures(t *testing.T) {
	for _, failedVenue := range supportedAccountVenues {
		for _, missing := range []bool{false, true} {
			for _, stream := range []bool{false, true} {
				t.Run(failedVenue+"/"+map[bool]string{true: "missing", false: "failed"}[missing]+"/"+map[bool]string{true: "sse", false: "ensure"}[stream], func(t *testing.T) {
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					bindings := make(map[string]string)
					factories := make(map[string]accountFeedFactory)
					for _, venue := range supportedAccountVenues {
						bindings[venue] = venue + "-owner"
						factory := &fakeAccountFeedFactory{snapshots: map[string]liveAccountSnapshot{
							bindings[venue]: connectedSnapshot(venue, bindings[venue], 100, 80),
						}}
						if venue == failedVenue {
							if missing {
								continue
							}
							factory.startErr = errors.New("test venue unavailable")
						}
						factories[venue] = factory
					}
					registry := newAccountFeedRegistry(ctx, factories, accountFeedRegistryConfig{})
					server := &Server{live: &LiveDeps{accounts: registry}}
					response := httptest.NewRecorder()
					if stream {
						query := url.Values{}
						for venue, owner := range bindings {
							query.Set("accounts["+venue+"]", owner)
						}
						requestCtx, stop := context.WithTimeout(ctx, 20*time.Millisecond)
						defer stop()
						server.handleLiveAccountEvents(response, httptest.NewRequest(http.MethodGet, "/api/v1/live/accounts/events?"+query.Encode(), nil).WithContext(requestCtx))
					} else {
						body, _ := json.Marshal(map[string]any{"accounts": bindings})
						server.handleLiveAccountsEnsure(response, httptest.NewRequest(http.MethodPost, "/api/v1/live/accounts/ensure", strings.NewReader(string(body))))
					}
					if response.Code != http.StatusOK {
						t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
					}
					body := response.Body.String()
					if stream {
						if response.Header().Get("Content-Type") != "text/event-stream" {
							t.Fatal("account stream was not opened")
						}
						_, body, _ = strings.Cut(body, "data: ")
						body, _, _ = strings.Cut(body, "\n")
					}
					var statuses map[string]venueAccountStatus
					if err := json.Unmarshal([]byte(body), &statuses); err != nil {
						t.Fatal(err)
					}
					for _, venue := range supportedAccountVenues {
						status, found := statuses[venue]
						if !found {
							t.Fatalf("missing %s status", venue)
						}
						if venue == failedVenue {
							if !status.Unavailable || status.Connected || status.StreamReady || status.Fresh || status.Reason == "" {
								t.Fatalf("failed venue not explicitly unavailable: %+v", status)
							}
						} else if !status.Fresh || !status.Connected || status.Equity != 100 {
							t.Fatalf("healthy venue affected by neighbor: %+v", status)
						}
					}
					registry.mu.Lock()
					defer registry.mu.Unlock()
					for _, entry := range registry.entries {
						if entry.refs != 0 {
							t.Fatalf("read-only handler leaked lease: %s refs=%d", entry.key, entry.refs)
						}
					}
				})
			}
		}
	}
}

func TestAccountEventsRecoverFailedVenueOnSameConnection(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	healthy := &fakeAccountFeedFactory{snapshots: map[string]liveAccountSnapshot{"sol-owner": connectedSnapshot("pacifica", "sol-owner", 100, 80)}}
	recovering := &recoveringAccountFactory{fakeAccountFeedFactory: fakeAccountFeedFactory{
		snapshots: map[string]liveAccountSnapshot{"phoenix-owner": connectedSnapshot("phoenix", "phoenix-owner", 200, 180)},
	}}
	recovering.unavailable.Store(true)
	registry := newAccountFeedRegistry(ctx, map[string]accountFeedFactory{"pacifica": healthy, "phoenix": recovering}, accountFeedRegistryConfig{})
	server := &Server{live: &LiveDeps{accounts: registry}}
	done := make(chan struct{})
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(done)
		server.handleLiveAccountEvents(w, r)
	}))
	defer httpServer.Close()
	requestCtx, stop := context.WithTimeout(ctx, 8*time.Second)
	defer stop()
	request, _ := http.NewRequestWithContext(requestCtx, http.MethodGet, httpServer.URL+"?accounts[pacifica]=sol-owner&accounts[phoenix]=phoenix-owner", nil)
	response, err := httpServer.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	scanner := bufio.NewScanner(response.Body)
	sawUnavailable, recovered := false, false
	for scanner.Scan() {
		data, ok := strings.CutPrefix(scanner.Text(), "data: ")
		if !ok {
			continue
		}
		var statuses map[string]venueAccountStatus
		if err := json.Unmarshal([]byte(data), &statuses); err != nil {
			t.Fatal(err)
		}
		if !statuses["pacifica"].Fresh || statuses["pacifica"].Equity != 100 {
			t.Fatal("healthy venue lost during neighboring outage", statuses)
		}
		if statuses["phoenix"].Unavailable {
			sawUnavailable = true
			recovering.unavailable.Store(false)
		} else if statuses["phoenix"].Fresh && statuses["phoenix"].Equity == 200 {
			recovered = true
			break
		}
	}
	if !sawUnavailable || !recovered || recovering.attempts.Load() != 2 || healthy.starts.Load() != 1 {
		t.Fatalf("unavailable=%t recovered=%t attempts=%d healthy starts=%d err=%v", sawUnavailable, recovered, recovering.attempts.Load(), healthy.starts.Load(), scanner.Err())
	}
	stop()
	_ = response.Body.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("account stream did not release its request")
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	for _, entry := range registry.entries {
		if entry.refs != 0 {
			t.Fatalf("recovered stream leaked lease: %s refs=%d", entry.key, entry.refs)
		}
	}
}

func TestAccountViewsKeepCapacityFailureLocalAndRetryWithBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	factory := &fakeAccountFeedFactory{}
	registry := newAccountFeedRegistry(ctx, map[string]accountFeedFactory{"pacifica": factory, "phoenix": factory}, accountFeedRegistryConfig{MaxPerVenue: 1})
	busy, err := registry.Acquire("phoenix", "other-owner")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Release()
	views := newAccountViews(registry, map[string]string{"pacifica": "sol-owner", "phoenix": "phoenix-owner"})
	defer views.accounts.Release()
	if views.snapshots()["phoenix"].UnavailableReason == "" || views.accounts.leases["pacifica"] == nil {
		t.Fatal("capacity failure affected healthy venue")
	}
	busy.Release()
	views.retryMissing(views.nextRetry.Add(-time.Nanosecond))
	if views.accounts.leases["phoenix"] != nil {
		t.Fatal("failed acquisition ignored retry backoff")
	}
	views.retryMissing(views.nextRetry)
	if views.snapshots()["phoenix"].UnavailableReason != "" || views.accounts.leases["phoenix"] == nil || factory.starts.Load() != 3 {
		t.Fatal("venue did not recover after capacity was released")
	}
}

func TestAccountViewsReportEveryUnavailableVenue(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	views := newAccountViews(newAccountFeedRegistry(ctx, nil, accountFeedRegistryConfig{}), map[string]string{
		"pacifica": "sol-owner", "phoenix": "phoenix-owner",
	})
	defer views.accounts.Release()
	statuses := accountViewStatuses(views.snapshots())
	if len(statuses) != 2 {
		t.Fatal("unavailable venues disappeared from the view")
	}
	for venue, status := range statuses {
		if !status.Unavailable || status.Fresh || status.Connected || status.Reason == "" {
			t.Fatalf("%s outage was hidden: %+v", venue, status)
		}
	}
}

func TestAccountViewsRejectUnsupportedBindingsBeforeAcquisition(t *testing.T) {
	server := newAccountScopedBalanceServer(t, nil, nil)
	for _, stream := range []bool{false, true} {
		response := httptest.NewRecorder()
		if stream {
			server.handleLiveAccountEvents(response, httptest.NewRequest(http.MethodGet, "/api/v1/live/accounts/events?accounts[pacifica]=sol-owner&accounts[unknown]=other-owner", nil))
		} else {
			server.handleLiveAccountsEnsure(response, httptest.NewRequest(http.MethodPost, "/api/v1/live/accounts/ensure", strings.NewReader(`{"accounts":{"pacifica":"sol-owner","unknown":"other-owner"}}`)))
		}
		if response.Code != http.StatusBadRequest {
			t.Fatalf("unsupported venue accepted: %d %s", response.Code, response.Body.String())
		}
	}
	if len(server.live.accounts.entries) != 0 {
		t.Fatal("invalid request started a healthy neighbor feed")
	}
}
