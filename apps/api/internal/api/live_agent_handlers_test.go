package api

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	hllive "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/hyperliquid/live"
	pacificlive "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/pacifica/live"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	secpECDSA "github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
	"github.com/mr-tron/base58"
)

type fakeHyperliquidAgentApprover struct {
	request hllive.ApproveAgentRequest
}

func (f *fakeHyperliquidAgentApprover) ApproveAgent(_ context.Context, request hllive.ApproveAgentRequest) error {
	f.request = request
	return nil
}

func TestHandleHyperliquidAgentApproveValidatesAndRelays(t *testing.T) {
	approver := &fakeHyperliquidAgentApprover{}
	server := &Server{live: &LiveDeps{hlAgentApprover: approver}}
	expected := signedHyperliquidApproval(t, time.Now().UnixMilli())
	body, err := json.Marshal(expected)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()

	server.handleHyperliquidAgentApprove(response, httptest.NewRequest(http.MethodPost, "/api/v1/live/agents/hyperliquid/approve", bytes.NewReader(body)))

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if approver.request.Action.AgentName != "Orbital Markets" {
		t.Fatalf("relayed action = %+v", approver.request.Action)
	}
}

func signedHyperliquidApproval(t *testing.T, nonce int64) hllive.ApproveAgentRequest {
	t.Helper()
	request := hllive.ApproveAgentRequest{
		OwnerAddress: "0x14791697260E4c9A71f18484C9f997B308e59325",
		Action: hllive.ApproveAgentAction{
			Type: "approveAgent", HyperliquidChain: "Mainnet", SignatureChainID: "0x1",
			AgentAddress: "0x19E7E376E7C213B7E7e7e46cc70A5dD086DAff2A",
			AgentName:    "Orbital Markets", Nonce: nonce,
		},
	}
	digest, err := request.SigningHash()
	if err != nil {
		t.Fatal(err)
	}
	privateKeyBytes, err := hex.DecodeString("0123456789012345678901234567890123456789012345678901234567890123")
	if err != nil {
		t.Fatal(err)
	}
	compact := secpECDSA.SignCompact(secp256k1.PrivKeyFromBytes(privateKeyBytes), digest[:], true)
	request.Signature = hllive.EthereumSignature{
		R: "0x" + hex.EncodeToString(compact[1:33]),
		S: "0x" + hex.EncodeToString(compact[33:]),
		V: int(compact[0]) - 4,
	}
	return request
}

func TestHandleHyperliquidAgentApproveRejectsPrivateKeyFields(t *testing.T) {
	approver := &fakeHyperliquidAgentApprover{}
	server := &Server{live: &LiveDeps{hlAgentApprover: approver}}
	body := []byte(`{"private_key":"secret"}`)
	response := httptest.NewRecorder()

	server.handleHyperliquidAgentApprove(response, httptest.NewRequest(http.MethodPost, "/api/v1/live/agents/hyperliquid/approve", bytes.NewReader(body)))

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", response.Code)
	}
}

type fakePacificaAgentBinder struct {
	request pacificlive.BindAgentRequest
}

func (f *fakePacificaAgentBinder) BindAgent(_ context.Context, request pacificlive.BindAgentRequest) error {
	f.request = request
	return nil
}

func TestHandlePacificaAgentBindVerifiesOwnerSignature(t *testing.T) {
	binder := &fakePacificaAgentBinder{}
	server := &Server{live: &LiveDeps{pacificaAgentBinder: binder}}
	request := validPacificaBindRequest(t, time.Now().UnixMilli())
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()

	server.handlePacificaAgentBind(response, httptest.NewRequest(http.MethodPost, "/api/v1/live/agents/pacifica/bind", bytes.NewReader(body)))

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if binder.request.AgentWallet != request.AgentWallet {
		t.Fatalf("relayed request = %+v", binder.request)
	}
}

func TestHandlePacificaAgentBindRejectsWrongOwnerSignature(t *testing.T) {
	binder := &fakePacificaAgentBinder{}
	server := &Server{live: &LiveDeps{pacificaAgentBinder: binder}}
	request := validPacificaBindRequest(t, time.Now().UnixMilli())
	request.Signature = base58.Encode(make([]byte, ed25519.SignatureSize))
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()

	server.handlePacificaAgentBind(response, httptest.NewRequest(http.MethodPost, "/api/v1/live/agents/pacifica/bind", bytes.NewReader(body)))

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", response.Code)
	}
}

func validPacificaBindRequest(t *testing.T, timestamp int64) pacificlive.BindAgentRequest {
	t.Helper()
	owner := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	agentSeed := make([]byte, ed25519.SeedSize)
	for index := range agentSeed {
		agentSeed[index] = byte(index + 32)
	}
	agent := ed25519.NewKeyFromSeed(agentSeed)
	message, err := pacificlive.BuildSigningMessage("bind_agent_wallet", timestamp, 5_000, map[string]any{
		"agent_wallet": base58.Encode(agent.Public().(ed25519.PublicKey)),
	})
	if err != nil {
		t.Fatal(err)
	}
	return pacificlive.BindAgentRequest{
		Account: ownerAddress(owner), Signature: base58.Encode(ed25519.Sign(owner, message)),
		Timestamp: timestamp, ExpiryWindow: 5_000,
		AgentWallet: base58.Encode(agent.Public().(ed25519.PublicKey)),
	}
}

func ownerAddress(privateKey ed25519.PrivateKey) string {
	return base58.Encode(privateKey.Public().(ed25519.PublicKey))
}
