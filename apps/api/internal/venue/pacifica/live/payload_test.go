package live

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
)

type testLotSizes map[string]string

func (m testLotSizes) LotSize(symbol string) (string, bool) {
	value, ok := m[symbol]
	return value, ok
}

var payloadTestLotSizes = testLotSizes{"BTC": "0.00001", "SOL": "0.01", "VIRTUAL": "0.1"}

func TestBuildClosePayloadPreservesResidualAmount(t *testing.T) {
	request, err := BuildClosePayload(payloadTestLotSizes, "redacted-wallet", "SOL", domain.SideLong, 2.75, 100, "redacted-client-id")
	if err != nil {
		t.Fatal(err)
	}
	if request.Amount != 2.75 || request.Action != "close" || !request.ReduceOnly {
		t.Fatalf("request summary = %+v, want reduce-only close amount 2.75", request)
	}
	var unsigned PacificaUnsignedOrder
	if err := json.Unmarshal(request.UnsignedPayload, &unsigned); err != nil {
		t.Fatal(err)
	}
	if unsigned.Amount != "2.75" || unsigned.Side != "ask" || unsigned.BuilderCode != OrbitalBuilder.Code {
		t.Fatalf("unsigned order = %+v, want ask amount 2.75", unsigned)
	}
}

func TestBuildRecoveryPayloadsOmitBuilderCode(t *testing.T) {
	builders := []struct {
		name   string
		action string
		build  func(LotSizeMap, string, string, domain.Side, float64, float64, string) (*domain.SigningRequest, error)
	}{
		{name: "unwind", action: "unwind", build: BuildUnwindPayload},
		{name: "emergency close", action: "emergency_close", build: BuildEmergencyClosePayload},
	}
	for _, test := range builders {
		t.Run(test.name, func(t *testing.T) {
			request, err := test.build(payloadTestLotSizes, "owner", "SOL", domain.SideLong, 2.75, 100, "client-id")
			if err != nil {
				t.Fatal(err)
			}
			var unsigned PacificaUnsignedOrder
			if err := json.Unmarshal(request.UnsignedPayload, &unsigned); err != nil {
				t.Fatal(err)
			}
			if request.Action != test.action || !request.ReduceOnly || unsigned.BuilderCode != "" {
				t.Fatalf("request = %+v, unsigned = %+v", request, unsigned)
			}
			wire, err := json.Marshal(unsigned)
			if err != nil || strings.Contains(string(wire), "builder_code") {
				t.Fatalf("wire payload includes builder code: %s (err=%v)", wire, err)
			}
		})
	}
}

func TestBuildOpenPayloadPreservesRequestedAmount(t *testing.T) {
	request, err := BuildOpenPayload(payloadTestLotSizes, "redacted-wallet", "BTC", domain.SideShort, 0.125, 90000, "redacted-client-id")
	if err != nil {
		t.Fatal(err)
	}
	if request.Amount != 0.125 || request.Action != "open" || request.ReduceOnly {
		t.Fatalf("request summary = %+v, want open amount 0.125", request)
	}
	var unsigned PacificaUnsignedOrder
	if err := json.Unmarshal(request.UnsignedPayload, &unsigned); err != nil {
		t.Fatal(err)
	}
	if unsigned.BuilderCode != "orbitalmarkets" {
		t.Fatalf("builder code = %q", unsigned.BuilderCode)
	}
}

func TestBuildOpenPayloadVenueExpiryCoversRequestLifetime(t *testing.T) {
	request, err := BuildOpenPayload(payloadTestLotSizes, "redacted-wallet", "BTC", domain.SideLong, 0.125, 90000, "redacted-client-id")
	if err != nil {
		t.Fatal(err)
	}

	var unsigned PacificaUnsignedOrder
	if err := json.Unmarshal(request.UnsignedPayload, &unsigned); err != nil {
		t.Fatal(err)
	}
	venueExpiry := time.UnixMilli(unsigned.Timestamp).Add(time.Duration(unsigned.ExpiryWindow) * time.Millisecond)
	if venueExpiry.Before(request.ExpiresAt) {
		t.Fatalf("venue payload expires at %s before signing request expires at %s", venueExpiry, request.ExpiresAt)
	}
	const (
		accountReconciliationTimeout = 20 * time.Second
		unwindTimeout                = 20 * time.Second
	)
	recoveryBudget := submitTimeout + accountReconciliationTimeout + unwindTimeout
	if recoveryWindow := venueExpiry.Sub(request.ExpiresAt); recoveryWindow < recoveryBudget {
		t.Fatalf("recovery window = %s, want at least %s", recoveryWindow, recoveryBudget)
	}
}

func TestAttachSignatureKeepsOwnerAndAgentIdentities(t *testing.T) {
	request := &domain.SigningRequest{Account: "owner-wallet", Signer: "agent-wallet"}
	order := AttachSignature(PacificaUnsignedOrder{Symbol: "BTC", BuilderCode: "orbitalmarkets"}, domain.SignedAction{
		SignerAddress: "agent-wallet", Signature: "signature",
	}, request)
	if order.Account != "owner-wallet" || order.AgentWallet != "agent-wallet" {
		t.Fatalf("order identities = account %q agent %q", order.Account, order.AgentWallet)
	}
	if order.BuilderCode != "orbitalmarkets" {
		t.Fatalf("builder code = %q", order.BuilderCode)
	}
}

func TestBuildOpenPayloadRoundsDownToPacificaLotSize(t *testing.T) {
	request, err := BuildOpenPayload(payloadTestLotSizes, "redacted-wallet", "VIRTUAL", domain.SideLong, 45.662100456621005, 1, "redacted-client-id")
	if err != nil {
		t.Fatal(err)
	}
	var unsigned PacificaUnsignedOrder
	if err := json.Unmarshal(request.UnsignedPayload, &unsigned); err != nil {
		t.Fatal(err)
	}
	if request.Amount != 45.6 || unsigned.Amount != "45.6" {
		t.Fatalf("normalized amount = %v (%q), want 45.6", request.Amount, unsigned.Amount)
	}
}

func TestBuildUpdateLeveragePayloadUsesCanonicalFields(t *testing.T) {
	request, err := BuildUpdateLeveragePayload("owner", "VIRTUAL", 2)
	if err != nil {
		t.Fatal(err)
	}
	var payload PacificaUnsignedLeverage
	if err := json.Unmarshal(request.UnsignedPayload, &payload); err != nil {
		t.Fatal(err)
	}
	if request.Action != "update_leverage" || request.Leverage != 2 || payload.Symbol != "VIRTUAL" || payload.Leverage != 2 {
		t.Fatalf("request = %+v payload = %+v", request, payload)
	}
}

func TestSubmitSignedOrderReturnsTransportFailureAsAmbiguous(t *testing.T) {
	request, err := BuildOpenPayload(payloadTestLotSizes, "redacted-wallet", "BTC", domain.SideLong, 0.125, 90000, "redacted-client-id")
	if err != nil {
		t.Fatal(err)
	}
	client := NewClient(slog.New(slog.NewTextHandler(io.Discard, nil)), nil, nil)
	tracker := NewTracker(slog.New(slog.NewTextHandler(io.Discard, nil)))
	client.sendSigned = func(context.Context, MarketOrderRequest) (*SubmitResult, error) {
		return nil, errors.New("write: connection timed out")
	}

	result, err := client.SubmitSignedOrder(context.Background(), domain.SignedAction{
		RequestID: request.ID, ClientOrderID: request.ClientOrderID, Venue: "pacifica",
		SignerAddress: "redacted-wallet", Signature: "redacted-signature",
	}, request, tracker)
	if err == nil || result != nil {
		t.Fatalf("result = %+v, err = %v; transport failure must be ambiguous", result, err)
	}
	tracker.mu.RLock()
	_, tracked := tracker.orders[request.ClientOrderID]
	tracker.mu.RUnlock()
	if !tracked {
		t.Fatal("ambiguous submission was not registered for fill reconciliation")
	}
}

func TestSubmitSignedOrderTracksFillArrivingBeforeSubmissionResponse(t *testing.T) {
	request, err := BuildOpenPayload(payloadTestLotSizes, "redacted-wallet", "SOL", domain.SideLong, 1, 100, "client-1")
	if err != nil {
		t.Fatal(err)
	}
	tracker := NewTracker(slog.New(slog.NewTextHandler(io.Discard, nil)))
	client := NewClient(slog.New(slog.NewTextHandler(io.Discard, nil)), nil, nil)
	client.sendSigned = func(_ context.Context, order MarketOrderRequest) (*SubmitResult, error) {
		tracker.HandleOrderUpdate([]byte(fmt.Sprintf(`[{"i":42,"I":%q,"s":"SOL","a":"1","f":"1","os":"filled","p":"100"}]`, order.ClientOrderID)))
		return &SubmitResult{
			OrderID: "42", ClientOrderID: order.ClientOrderID, Symbol: order.Symbol,
			Accepted: true, SubmittedAt: time.Now(), RespondedAt: time.Now(),
		}, nil
	}

	if _, err := client.SubmitSignedOrder(context.Background(), domain.SignedAction{
		RequestID: request.ID, ClientOrderID: request.ClientOrderID, Venue: "pacifica",
		SignerAddress: "redacted-wallet", Signature: "redacted-signature",
	}, request, tracker); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	fill, err := tracker.WaitForFill(ctx, request.ClientOrderID)
	if err != nil {
		t.Fatal(err)
	}
	if fill.Status != OrderStatusFilled || fill.FilledAmount != 1 || fill.AvgFillPrice != 100 {
		t.Fatalf("fill = %+v, want pre-response fill event retained", fill)
	}
}
