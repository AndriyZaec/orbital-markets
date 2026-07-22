package api

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/executor"
)

func TestDurableLiveSessionRoundTripPreservesRecoveryMaterial(t *testing.T) {
	createdAt := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	req := &domain.SigningRequest{
		ID: "unwind-request", ClientOrderID: "unwind-cloid", Venue: "pacifica",
		Action: "close", Symbol: "SOL", Side: "sell", Amount: 10, ReduceOnly: true,
		UnsignedPayload: json.RawMessage(`{"order":"payload"}`), ExpiresAt: createdAt.Add(2 * time.Minute),
	}
	signed := &domain.SignedAction{
		RequestID: req.ID, ClientOrderID: req.ClientOrderID, Venue: req.Venue,
		SignerAddress: "wallet", Signature: "signature",
	}
	session := &LiveSession{
		ID:              "session-1",
		Plan:            &domain.ExecutionPlan{ID: "plan-1", Asset: "SOL", Notional: 10},
		Leg1:            legPlan{venue: "pacifica", symbol: "SOL", side: domain.SideLong, price: 100},
		Leg2:            legPlan{venue: "hyperliquid", symbol: "SOL", side: domain.SideShort, price: 101},
		AccountPacifica: "sol-wallet", AccountHyperliquid: "0xwallet",
		State: sessAwaitingLeg2Sign, BaselineLeg1Size: 3, BaselineLeg2Size: -2,
		Leg1OpenReq: req, Leg1UnwindReq: req,
		ArmedUnwindReq: req, ArmedUnwindSigned: signed,
		Leg1Fill:     &normFill{FilledAmount: 10, AvgFillPrice: 100, Filled: true},
		Leg2Attempts: 1,
		Recovery:     []executor.RecoveryAction{{Action: "retry_leg2", Detail: "residual=6"}},
		CreatedAt:    createdAt, UpdatedAt: createdAt.Add(time.Minute),
	}

	payload, err := marshalLiveSession(session)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := unmarshalLiveSession(payload)
	if err != nil {
		t.Fatal(err)
	}
	if restored.State != sessAwaitingLeg2Sign || restored.BaselineLeg1Size != 3 || restored.BaselineLeg2Size != -2 {
		t.Fatalf("restored state = %q baselines = %v/%v", restored.State, restored.BaselineLeg1Size, restored.BaselineLeg2Size)
	}
	if restored.ArmedUnwindSigned == nil || restored.ArmedUnwindSigned.Signature != "signature" {
		t.Fatalf("armed signed action not restored: %+v", restored.ArmedUnwindSigned)
	}
	if restored.ArmedUnwindReq == nil || string(restored.ArmedUnwindReq.UnsignedPayload) != `{"order":"payload"}` {
		t.Fatalf("armed request not restored: %+v", restored.ArmedUnwindReq)
	}
	if restored.Leg1Fill == nil || restored.Leg1Fill.FilledAmount != 10 {
		t.Fatalf("leg 1 fill not restored: %+v", restored.Leg1Fill)
	}
	if restored.Leg2Attempts != 1 || len(restored.Recovery) != 1 || restored.Recovery[0].Action != "retry_leg2" {
		t.Fatalf("retry recovery state not restored: attempts=%d recovery=%+v", restored.Leg2Attempts, restored.Recovery)
	}
}

func TestSessionManagerReturnsExpiredSessionsForRecovery(t *testing.T) {
	manager := NewSessionManager()
	session := &LiveSession{
		ID: "exposed-session", State: sessAwaitingLeg2Sign,
		CreatedAt: time.Now().Add(-sessionTTL - time.Second),
	}
	manager.put(session)

	if _, found, claimed := manager.claim(session.ID); !found || !claimed {
		t.Fatal("expired exposed session was silently evicted by claim")
	}
	manager.release(session.ID)
	expired := manager.takeExpired()
	if len(expired) != 1 || expired[0].ID != session.ID {
		t.Fatalf("expired sessions = %+v, want exposed-session", expired)
	}
	if _, found, _ := manager.claim(session.ID); found {
		t.Fatal("claimed expired session remained in manager")
	}
}

func TestSessionManagerAllowsOnlyOneInFlightAction(t *testing.T) {
	manager := NewSessionManager()
	manager.put(&LiveSession{ID: "session-1", CreatedAt: time.Now()})
	if _, found, claimed := manager.claim("session-1"); !found || !claimed {
		t.Fatal("first action did not claim session")
	}
	if _, found, claimed := manager.claim("session-1"); !found || claimed {
		t.Fatal("second concurrent action claimed session")
	}
	manager.release("session-1")
	if _, found, claimed := manager.claim("session-1"); !found || !claimed {
		t.Fatal("session was not claimable after release")
	}
}
