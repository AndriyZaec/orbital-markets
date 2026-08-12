package live

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAgentApproverRelaysOfficialWireShape(t *testing.T) {
	nonce := time.Now().UnixMilli()
	var received map[string]any
	venue := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","response":{"type":"default"}}`))
	}))
	defer venue.Close()

	approver := NewAgentApprover(venue.URL, venue.Client())
	err := approver.ApproveAgent(context.Background(), validApproveAgentRequest(nonce))
	if err != nil {
		t.Fatal(err)
	}
	if received["nonce"] != float64(nonce) {
		t.Fatalf("outer nonce = %v, want %d", received["nonce"], nonce)
	}
	if _, ok := received["owner_address"]; ok {
		t.Fatal("relay sent owner_address to Hyperliquid")
	}
	if len(received) != 5 {
		t.Fatalf("wire body fields = %v", received)
	}
}

func TestAgentApproverPropagatesVenueRejection(t *testing.T) {
	venue := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"status":"err","response":"invalid signature"}`))
	}))
	defer venue.Close()

	approver := NewAgentApprover(venue.URL, venue.Client())
	err := approver.ApproveAgent(context.Background(), validApproveAgentRequest(time.Now().UnixMilli()))
	if err == nil || err.Error() != "Hyperliquid rejected agent approval: invalid signature" {
		t.Fatalf("error = %v", err)
	}
}

func TestApproveAgentRequestVerifiesOfficialOwnerSignature(t *testing.T) {
	const nonce = int64(1_748_970_123_456)
	request := validApproveAgentRequest(nonce)
	if err := request.Validate(time.UnixMilli(nonce)); err != nil {
		t.Fatalf("official signature rejected: %v", err)
	}
	request.OwnerAddress = "0x0000000000000000000000000000000000000001"
	if err := request.Validate(time.UnixMilli(nonce)); err == nil {
		t.Fatal("signature was accepted for the wrong owner")
	}
}

func validApproveAgentRequest(nonce int64) ApproveAgentRequest {
	return ApproveAgentRequest{
		OwnerAddress: "0x14791697260E4c9A71f18484C9f997B308e59325",
		Action: ApproveAgentAction{
			Type: "approveAgent", HyperliquidChain: "Mainnet", SignatureChainID: "0x1",
			AgentAddress: "0x19E7E376E7C213B7E7e7e46cc70A5dD086DAff2A",
			AgentName:    "Orbital Markets", Nonce: nonce,
		},
		Signature: EthereumSignature{
			R: "0xafb2eeb78847d03955929890b3f6371feda37b33a14c7e7dbab45f433457c56",
			S: "0x67f8856879f44dcf164fd86c9fe58c084077aa5221ba31412951be7749e0351c",
			V: 27,
		},
	}
}
