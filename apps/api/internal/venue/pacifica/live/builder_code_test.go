package live

import (
	"crypto/ed25519"
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
