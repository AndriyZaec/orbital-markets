package api

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	hllive "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/hyperliquid/live"
	pacificlive "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/pacifica/live"
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
	body := []byte(`{
		"action":{"type":"approveAgent","hyperliquidChain":"Mainnet","signatureChainId":"0x1","agentAddress":"0x19E7E376E7C213B7E7e7e46cc70A5dD086DAff2A","agentName":"Orbital Markets","nonce":` +
		formatInt(time.Now().UnixMilli()) + `},
		"signature":{"r":"0x83aa677ba1e5d3c7f7cf727002326d724aaa18943f840cb20fa24c360ff5725a","s":"0x6d62d993d2436db5ce557ec8608624fcb8d4e4410354880e11d99af34a33790","v":27}
	}`)
	response := httptest.NewRecorder()

	server.handleHyperliquidAgentApprove(response, httptest.NewRequest(http.MethodPost, "/api/v1/live/agents/hyperliquid/approve", bytes.NewReader(body)))

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if approver.request.Action.AgentName != "Orbital Markets" {
		t.Fatalf("relayed action = %+v", approver.request.Action)
	}
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

func formatInt(value int64) string {
	return strconv.FormatInt(value, 10)
}
