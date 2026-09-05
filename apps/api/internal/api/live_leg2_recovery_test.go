package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
)

func TestMergeNormFillsCalculatesAggregateHedge(t *testing.T) {
	first := &normFill{FilledAmount: 4, AvgFillPrice: 100, Fee: 0.4, Filled: true}
	retry := &normFill{FilledAmount: 5.7, AvgFillPrice: 102, Fee: 0.6, Filled: true}

	merged := mergeNormFills(first, retry)
	if merged.FilledAmount != 9.7 {
		t.Fatalf("filled amount = %v, want 9.7", merged.FilledAmount)
	}
	wantPrice := (4*100 + 5.7*102) / 9.7
	if diff := merged.AvgFillPrice - wantPrice; diff < -1e-9 || diff > 1e-9 {
		t.Fatalf("average price = %v, want %v", merged.AvgFillPrice, wantPrice)
	}
	if mismatch := hedgeMismatch(10, merged.FilledAmount); mismatch > maxHedgeMismatchPct {
		t.Fatalf("mismatch = %v, want within tolerance", mismatch)
	}
}

func TestRemainingFillAfterPartialUnwindTracksExactExposure(t *testing.T) {
	fill := &normFill{FilledAmount: 10, AvgFillPrice: 100, Filled: true}
	remaining := remainingFillAfterUnwind(fill, 7.25)
	if remaining == nil || remaining.FilledAmount != 2.75 {
		t.Fatalf("remaining fill = %+v, want 2.75", remaining)
	}
	if fullyClosed := remainingFillAfterUnwind(fill, 10); fullyClosed != nil {
		t.Fatalf("fully closed fill = %+v, want nil", fullyClosed)
	}
}

func TestRetryableLeg2FillAllowsKnownZeroFillButNotTimeout(t *testing.T) {
	if !retryableLeg2Fill(&normFill{Status: "rejected"}, 10) {
		t.Fatal("known zero-fill rejection should receive one retry")
	}
	if retryableLeg2Fill(&normFill{Status: "timeout"}, 10) {
		t.Fatal("ambiguous timeout should reconcile instead of retrying blindly")
	}
}

func TestUnwindConfirmationDoesNotHideResidualExposure(t *testing.T) {
	if unwindFullyFilled(10, 9.96) {
		t.Fatal("99.6% unwind must preserve the remaining exposure")
	}
	if !unwindFullyFilled(10, 10) {
		t.Fatal("full unwind was not confirmed")
	}
}

func TestRetryMinimumAppliesOnlyToNormalizedHyperliquidNotional(t *testing.T) {
	if !retryBelowMinimumNotional("hyperliquid", 0.099, 100) {
		t.Fatal("Hyperliquid retry below $10 should be suppressed")
	}
	if retryBelowMinimumNotional("hyperliquid", 0.1, 100) {
		t.Fatal("Hyperliquid retry at $10 should be allowed")
	}
	if retryBelowMinimumNotional("pacifica", 0.01, 100) {
		t.Fatal("Hyperliquid minimum must not suppress Pacifica retries")
	}
}

func TestCompleteHedgeOpenReportsRecoveringWhenTerminalPersistenceFails(t *testing.T) {
	server := &Server{
		live:   &LiveDeps{sessions: NewSessionManager()},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	session := &LiveSession{
		ID:   "session-1",
		Plan: &domain.ExecutionPlan{ID: "position-1"},
	}
	recorder := httptest.NewRecorder()

	server.completeHedgeOpen(recorder, context.Background(), session, 0)

	var response map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response["status"] != string(sessRecovering) {
		t.Fatalf("status = %v, want %s", response["status"], sessRecovering)
	}
	if _, exists := response["position_id"]; exists {
		t.Fatalf("position_id must not be reported before terminal persistence: %v", response["position_id"])
	}
}

func TestSigningClientOrderIDUsesVenueFacingIdentifier(t *testing.T) {
	request := &domain.SigningRequest{ID: "signing-request", ClientOrderID: "venue-client-order"}
	if got := signingClientOrderID(request, request.ID); got != request.ClientOrderID {
		t.Fatalf("client order ID = %q, want %q", got, request.ClientOrderID)
	}
}
