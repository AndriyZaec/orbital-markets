package api

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	hllive "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/hyperliquid/live"
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

func formatInt(value int64) string {
	return strconv.FormatInt(value, 10)
}
