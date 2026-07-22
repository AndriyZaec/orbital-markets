package live

import (
	"encoding/json"
	"testing"

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
