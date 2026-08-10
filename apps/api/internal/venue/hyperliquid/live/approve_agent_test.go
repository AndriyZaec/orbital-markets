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

func validApproveAgentRequest(nonce int64) ApproveAgentRequest {
	return ApproveAgentRequest{
		Action: ApproveAgentAction{
			Type: "approveAgent", HyperliquidChain: "Mainnet", SignatureChainID: "0x1",
			AgentAddress: "0x19E7E376E7C213B7E7e7e46cc70A5dD086DAff2A",
			AgentName:    "Orbital Markets", Nonce: nonce,
		},
		Signature: EthereumSignature{
			R: "0x83aa677ba1e5d3c7f7cf727002326d724aaa18943f840cb20fa24c360ff5725a",
			S: "0x6d62d993d2436db5ce557ec8608624fcb8d4e4410354880e11d99af34a33790",
			V: 27,
		},
	}
}
