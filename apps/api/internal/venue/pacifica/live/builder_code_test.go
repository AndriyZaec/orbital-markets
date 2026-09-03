package live

import (
	"context"
	"crypto/ed25519"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mr-tron/base58"
)

func TestApproveBuilderCodeRequestValidatesConfiguredBuilder(t *testing.T) {
	owner := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	timestamp := time.Now().UnixMilli()
	message, err := BuildSigningMessage("approve_builder_code", timestamp, builderApprovalExpiry, map[string]any{
		"builder_code": OrbitalBuilder.Code,
		"max_fee_rate": OrbitalBuilder.MaxFeeRate,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := ApproveBuilderCodeRequest{
		Account: base58.Encode(owner.Public().(ed25519.PublicKey)), Signature: base58.Encode(ed25519.Sign(owner, message)),
		Timestamp: timestamp, ExpiryWindow: builderApprovalExpiry,
		BuilderCode: OrbitalBuilder.Code, MaxFeeRate: OrbitalBuilder.MaxFeeRate,
	}
	if err := request.Validate(time.UnixMilli(timestamp), OrbitalBuilder); err != nil {
		t.Fatal(err)
	}
	request.MaxFeeRate = "0.001"
	if err := request.Validate(time.UnixMilli(timestamp), OrbitalBuilder); err == nil {
		t.Fatal("unexpected max fee rate was accepted")
	}
}

func TestHasBuilderCodeApprovalAcceptsSufficientAllowance(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Query().Get("account") != OrbitalBuilder.Owner {
			t.Fatalf("request = %s %s", r.Method, r.URL.String())
		}
		_, _ = w.Write([]byte(`[
			{"builder_code":"other","max_fee_rate":"1"},
			{"builder_code":"orbitalmarkets","max_fee_rate":"0.0002"}
		]`))
	}))
	defer server.Close()
	client := NewBuilderCodeApprover(server.URL, server.URL, server.Client())

	approved, err := client.HasBuilderCodeApproval(context.Background(), OrbitalBuilder.Owner, OrbitalBuilder)
	if err != nil {
		t.Fatal(err)
	}
	if !approved {
		t.Fatal("sufficient builder allowance was not recognized")
	}
}
