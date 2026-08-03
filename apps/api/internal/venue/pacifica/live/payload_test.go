package live

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
)

func TestBuildClosePayloadPreservesResidualAmount(t *testing.T) {
	request, err := BuildClosePayload("redacted-wallet", "SOL", domain.SideLong, 2.75, 100, "redacted-client-id")
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
	if unsigned.Amount != "2.75" || unsigned.Side != "ask" {
		t.Fatalf("unsigned order = %+v, want ask amount 2.75", unsigned)
	}
}

func TestBuildOpenPayloadPreservesRequestedAmount(t *testing.T) {
	request, err := BuildOpenPayload("redacted-wallet", "BTC", domain.SideShort, 0.125, 90000, "redacted-client-id")
	if err != nil {
		t.Fatal(err)
	}
	if request.Amount != 0.125 || request.Action != "open" || request.ReduceOnly {
		t.Fatalf("request summary = %+v, want open amount 0.125", request)
	}
}

func TestBuildOpenPayloadVenueExpiryCoversRequestLifetime(t *testing.T) {
	request, err := BuildOpenPayload("redacted-wallet", "BTC", domain.SideLong, 0.125, 90000, "redacted-client-id")
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

func TestSubmitSignedOrderReturnsTransportFailureAsAmbiguous(t *testing.T) {
	request, err := BuildOpenPayload("redacted-wallet", "BTC", domain.SideLong, 0.125, 90000, "redacted-client-id")
	if err != nil {
		t.Fatal(err)
	}
	client := NewClient(slog.New(slog.NewTextHandler(io.Discard, nil)), nil, nil)
	client.sendSigned = func(context.Context, MarketOrderRequest) (*SubmitResult, error) {
		return nil, errors.New("write: connection timed out")
	}

	result, err := client.SubmitSignedOrder(context.Background(), domain.SignedAction{
		RequestID: request.ID, ClientOrderID: request.ClientOrderID, Venue: "pacifica",
		SignerAddress: "redacted-wallet", Signature: "redacted-signature",
	}, request, nil)
	if err == nil || result != nil {
		t.Fatalf("result = %+v, err = %v; transport failure must be ambiguous", result, err)
	}
}
