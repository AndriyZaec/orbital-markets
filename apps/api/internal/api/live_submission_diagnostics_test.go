package api

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/executor"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
)

type diagnosticRetryModule struct{ *fakeLiveModule }

func (m diagnosticRetryModule) BuildOpen(p venue.LiveOrderParams) (*domain.SigningRequest, error) {
	return &domain.SigningRequest{ID: "retry-request", ClientOrderID: p.ClientOrderID, Venue: m.Name(),
		Action: "open", Account: p.Account, Signer: p.Signer, Symbol: p.Symbol, Amount: p.Amount,
		Price: p.Price, CreatedAt: time.Now(), ExpiresAt: time.Now().Add(30 * time.Second)}, nil
}

func TestHedgeRejectionReasonsSurviveInitialAndRetryFlowsWithoutPublicExposure(t *testing.T) {
	s, _ := newResidualExposureServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var logs bytes.Buffer
	s.logger = slog.New(slog.NewTextHandler(&logs, nil))
	s.recoveryOwner = "rejection-test"
	s.live.sessions = NewSessionManager()
	s.live.modules, _ = venue.NewLiveModuleRegistry(diagnosticRetryModule{&fakeLiveModule{name: "phoenix"}})
	feed := &fakeAccountFeed{submitResult: &domain.SubmissionResult{Error: "Phoenix order failed", PrivateDiagnostic: "initial SDK rejection detail"}}
	s.live.accounts = newAccountFeedRegistry(ctx, map[string]accountFeedFactory{
		"pacifica": &fixedAccountFeedFactory{feed: &fakeAccountFeed{}}, "phoenix": &fixedAccountFeedFactory{feed: feed},
	}, accountFeedRegistryConfig{})
	bindings := liveVenueBindings{Accounts: map[string]string{"pacifica": "owner", "phoenix": "owner"}, Agents: map[string]string{"phoenix": "agent"}}
	accounts, err := s.live.acquireAccountContext(bindings.Accounts, true)
	if err != nil {
		t.Fatal(err)
	}
	defer accounts.Release()
	now := time.Now()
	request := &domain.SigningRequest{ID: "initial-request", ClientOrderID: "initial-order", Venue: "phoenix",
		Action: "open", Account: "owner", Signer: "agent", Amount: 1493, ExpiresAt: now.Add(time.Minute)}
	session := &LiveSession{ID: "rejected-hedge", State: sessAwaitingLeg2Sign, Bindings: bindings,
		Plan: &domain.ExecutionPlan{ID: "rejected-position", Asset: "CHIP", Notional: 50,
			Leverage: domain.LeverageConfig{Leverage: 2}, Leg1: domain.Leg{Venue: "pacifica"}, Leg2: domain.Leg{Venue: "phoenix"}},
		Leg1:        legPlan{venue: "pacifica", symbol: "CHIP", side: domain.SideShort},
		Leg2:        legPlan{venue: "phoenix", symbol: "CHIP", side: domain.SideLong},
		Leg1Fill:    &normFill{Filled: true, FilledAmount: 1493, AvgFillPrice: 0.0334},
		Leg2OpenReq: request, Leg2OpenReqID: request.ID, accounts: accounts, CreatedAt: now, UpdatedAt: now}
	payload, _ := marshalLiveSession(session)
	if err := s.liveStore.UpsertDurableSession(ctx, executor.DurableSessionRecord{ID: session.ID, State: string(session.State),
		Payload: payload, AccountBindings: bindings.Accounts, Asset: "CHIP", HasExposure: true,
		CreatedAt: now, ExpiresAt: now.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	s.live.signingStore.Store(request)
	response := httptest.NewRecorder()
	s.advanceLeg2(response, httptest.NewRequest("POST", "/", nil).WithContext(ctx), session, []domain.SignedAction{{
		RequestID: request.ID, ClientOrderID: request.ClientOrderID, Venue: "phoenix", SignerAddress: "agent", Signature: "test-signature",
	}}, nil)
	if session.State != sessAwaitingLeg2RetrySign {
		t.Fatalf("initial rejection did not reach bounded retry: %s", response.Body.String())
	}
	firstResponse := response.Body.String()
	feed.submitResult = &domain.SubmissionResult{Error: "retry SDK rejection detail"}
	retry := session.Leg2RetryReq
	response = httptest.NewRecorder()
	s.advanceLeg2Retry(response, httptest.NewRequest("POST", "/", nil).WithContext(ctx), session, []domain.SignedAction{{
		RequestID: retry.ID, ClientOrderID: retry.ClientOrderID, Venue: "phoenix", SignerAddress: "agent", Signature: "test-signature",
	}}, nil)
	record, err := s.liveStore.GetDurableSession(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := unmarshalLiveSession(record.Payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored.SubmissionRejections) != 2 {
		t.Fatalf("rejection diagnostics lost across durable serialization: %+v", restored.SubmissionRejections)
	}
	status, err := s.liveSessionStatusSnapshot(ctx, session.ID, bindings.Accounts)
	if err != nil {
		t.Fatal(err)
	}
	publicStatus, _ := json.Marshal(status)
	events, err := s.liveStore.GetEvents(ctx, session.Plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	publicEvents, _ := json.Marshal(events)
	for i, reason := range []string{"initial SDK rejection detail", "retry SDK rejection detail"} {
		diagnostic := restored.SubmissionRejections[i]
		if diagnostic.Reason != reason || diagnostic.Attempt != i+1 || diagnostic.Venue != "phoenix" || diagnostic.RequestID == "" {
			t.Fatalf("wrong rejection diagnostic: %+v", diagnostic)
		}
		if !strings.Contains(logs.String(), reason) {
			t.Fatalf("operator log lost reason: %s", reason)
		}
		if strings.Contains(firstResponse+response.Body.String()+string(publicStatus)+string(publicEvents), reason) {
			t.Fatalf("private rejection detail leaked into public response/events: %s", reason)
		}
	}
}
