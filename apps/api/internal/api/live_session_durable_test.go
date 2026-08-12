package api

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/executor"
	paclive "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/pacifica/live"
)

type recoveryTestLotSizes struct{}

func (recoveryTestLotSizes) LotSize(string) (string, bool) { return "0.01", true }

func TestDurableLiveSessionRoundTripPreservesRecoveryMaterial(t *testing.T) {
	createdAt := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	req := &domain.SigningRequest{
		ID: "unwind-request", ClientOrderID: "unwind-cloid", Venue: "pacifica",
		Signer: "sol-agent",
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
		AgentPacifica: "sol-agent", AgentHyperliquid: "0xagent",
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
	if restored.ArmedUnwindReq.Account != "sol-wallet" {
		t.Fatalf("armed request account = %q, want persisted session account", restored.ArmedUnwindReq.Account)
	}
	if restored.ArmedUnwindReq.Signer != "sol-agent" {
		t.Fatalf("armed request signer = %q, want persisted session agent", restored.ArmedUnwindReq.Signer)
	}
	if restored.AgentPacifica != "sol-agent" || restored.AgentHyperliquid != "0xagent" {
		t.Fatalf("agent identities not restored: %q/%q", restored.AgentPacifica, restored.AgentHyperliquid)
	}
	if !agentBoundLeg1Requests(restored) {
		t.Fatal("restored leg-1 requests lost their agent binding")
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

func TestPacificaUnwindRemainsValidThroughSessionRecoveryBudget(t *testing.T) {
	request, err := paclive.BuildClosePayload(
		recoveryTestLotSizes{},
		"owner", "SOL", domain.SideLong, 1, 100, "recovery-window-test",
	)
	if err != nil {
		t.Fatal(err)
	}
	var unsigned paclive.PacificaUnsignedOrder
	if err := json.Unmarshal(request.UnsignedPayload, &unsigned); err != nil {
		t.Fatal(err)
	}
	venueExpiry := time.UnixMilli(unsigned.Timestamp).Add(time.Duration(unsigned.ExpiryWindow) * time.Millisecond)
	const recoveryBudget = 50 * time.Second
	if required := request.CreatedAt.Add(sessionTTL + recoveryBudget); venueExpiry.Before(required) {
		t.Fatalf("Pacifica unwind expires at %s before recovery budget %s", venueExpiry, required)
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
