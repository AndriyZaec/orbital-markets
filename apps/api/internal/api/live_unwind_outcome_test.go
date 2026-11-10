package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/executor"
)

func TestInvalidHedgeUnwindOutcomeMatchesDurableSessionStatus(t *testing.T) {
	for _, tc := range []struct {
		name, status, unwind string
		accepted             bool
		closed, hedge        float64
	}{
		{"fully closed", "aborted", "confirmed", true, 1493, 0},
		{"partial close", "degraded", "unconfirmed", true, 1000, 0},
		{"close rejected", "degraded", "submit_failed", false, 0, 0},
		{"hedge remains", "degraded", "confirmed", true, 1493, 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := newResidualExposureServer(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s.recoveryOwner = "unwind-outcome-test"
			s.live.sessions = NewSessionManager()
			feed := &fakeAccountFeed{submitResult: &domain.SubmissionResult{Accepted: tc.accepted},
				waitFill: &normFill{Filled: tc.closed > 0, FilledAmount: tc.closed, AvgFillPrice: 0.0336}}
			s.live.accounts = newAccountFeedRegistry(ctx, map[string]accountFeedFactory{
				"pacifica": &fixedAccountFeedFactory{feed: feed}, "phoenix": &fixedAccountFeedFactory{feed: &fakeAccountFeed{}},
			}, accountFeedRegistryConfig{})
			bindings := liveVenueBindings{Accounts: map[string]string{"pacifica": "owner", "phoenix": "owner"}}
			accounts, err := s.live.acquireAccountContext(bindings.Accounts, true)
			if err != nil {
				t.Fatal(err)
			}
			defer accounts.Release()
			now := time.Now()
			session := &LiveSession{ID: "unwind-session", State: sessLeg2Submitting, Bindings: bindings,
				Plan: &domain.ExecutionPlan{ID: "unwind-position", Asset: "CHIP", Notional: 50,
					Leverage: domain.LeverageConfig{Leverage: 2}, Leg1: domain.Leg{Venue: "pacifica"}, Leg2: domain.Leg{Venue: "phoenix"}},
				Leg1:     legPlan{venue: "pacifica", symbol: "CHIP", side: domain.SideShort},
				Leg2:     legPlan{venue: "phoenix", symbol: "CHIP", side: domain.SideLong},
				Leg1Fill: &normFill{Filled: true, FilledAmount: 1493, AvgFillPrice: 0.0334},
				ArmedUnwindReq: &domain.SigningRequest{ID: "unwind", Venue: "pacifica", Account: "owner", Signer: "agent",
					Action: "unwind", Amount: 1493, ReduceOnly: true, ExpiresAt: now.Add(time.Minute)},
				ArmedUnwindSigned: &domain.SignedAction{SignerAddress: "agent"}, accounts: accounts, CreatedAt: now, UpdatedAt: now}
			if tc.hedge > 0 {
				session.Leg2Fill = &normFill{Filled: true, FilledAmount: tc.hedge}
			}
			payload, err := marshalLiveSession(session)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.liveStore.UpsertDurableSession(ctx, executor.DurableSessionRecord{ID: session.ID, State: string(session.State),
				Payload: payload, AccountBindings: bindings.Accounts, Asset: "CHIP", HasExposure: true,
				CreatedAt: now, ExpiresAt: now.Add(time.Minute)}); err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			s.recoverInvalidHedge(response, ctx, session, "leg-2 retry rejected")
			var body struct {
				Status    string `json:"status"`
				Unwind    string `json:"unwind_status"`
				Remaining []any  `json:"remaining_exposure"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Status != tc.status || body.Unwind != tc.unwind {
				t.Fatalf("immediate response = %s, want %s/%s", response.Body.String(), tc.status, tc.unwind)
			}
			stored, err := s.liveSessionStatusSnapshot(ctx, session.ID, bindings.Accounts)
			if err != nil {
				t.Fatal(err)
			}
			if stored["status"] != tc.status {
				t.Fatalf("HTTP/SSE session snapshot disagrees with immediate response: %+v", stored)
			}
			if tc.status == "aborted" && (stored["unwind_status"] != "confirmed" || len(body.Remaining) != 0) {
				t.Fatalf("clean abort lost confirmed closure evidence: %+v", stored)
			}
			if tc.status == "aborted" && stored["leg1_fill"].(map[string]any)["filled_amount"] != float64(1493) {
				t.Fatalf("clean abort erased its historical fill: %+v", stored)
			}
			if tc.status == "degraded" && len(body.Remaining) == 0 {
				t.Fatal("remaining position was hidden")
			}
		})
	}
}
