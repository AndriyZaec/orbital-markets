package api

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/executor"
	asterlive "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/aster/live"
	hllive "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/hyperliquid/live"
	pacificlive "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/pacifica/live"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	secpECDSA "github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
	"github.com/mr-tron/base58"
)

type fakeHyperliquidAgentApprover struct {
	request hllive.ApproveAgentRequest
}

type fakeAsterAgentApprover struct {
	request asterlive.ApproveAgentRequest
	err     error
}

func (f *fakeAsterAgentApprover) ApproveAgent(_ context.Context, request asterlive.ApproveAgentRequest) error {
	f.request = request
	return f.err
}

func TestHandleAsterAgentApproveReportsUncertainOutcome(t *testing.T) {
	now := time.Now()
	approver := &fakeAsterAgentApprover{err: asterlive.ErrSubmissionAmbiguous}
	server := &Server{logger: slog.Default(), live: &LiveDeps{
		asterAgentApprover: approver,
		asterBuilder:       &asterlive.BuilderConfig{Address: "0xe625a2d279815749c647daed24df41bc8dd14bfe", FeeRate: "0.0002"},
		accounts:           &accountFeedRegistry{factories: map[string]accountFeedFactory{}},
	}}
	body, err := json.Marshal(validHTTPAsterAgentApproval(now))
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	server.handleAsterAgentApprove(response, httptest.NewRequest(
		http.MethodPost, "/api/v1/live/agents/aster/approve", bytes.NewReader(body),
	))
	if response.Code != http.StatusAccepted || !strings.Contains(response.Body.String(), `"status":"uncertain"`) {
		t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
	}
}

func TestHandleAsterAgentReconcileSelectsVenueAgent(t *testing.T) {
	probe := &fakeAsterDataAgentProbe{}
	server := &Server{
		asterDataAgent: probe,
		live: &LiveDeps{accounts: &accountFeedRegistry{
			factories: map[string]accountFeedFactory{},
		}},
	}
	response := httptest.NewRecorder()
	server.handleAsterAgentReconcile(response, httptest.NewRequest(
		http.MethodPost, "/api/v1/live/agents/aster/reconcile",
		bytes.NewBufferString(`{"account":"0x1111111111111111111111111111111111111111","candidates":["0x2222222222222222222222222222222222222222"]}`),
	))
	if response.Code != http.StatusOK || probe.executor != "0x2222222222222222222222222222222222222222" ||
		!strings.Contains(response.Body.String(), `"agent_address":"0x2222222222222222222222222222222222222222"`) {
		t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
	}
}

func TestHandleAsterAgentApproveValidatesAndRelays(t *testing.T) {
	now := time.Now()
	approver := &fakeAsterAgentApprover{}
	server := &Server{live: &LiveDeps{
		asterAgentApprover: approver,
		asterBuilder:       &asterlive.BuilderConfig{Address: "0xe625a2d279815749c647daed24df41bc8dd14bfe", FeeRate: "0.0002"},
		accounts:           &accountFeedRegistry{factories: map[string]accountFeedFactory{}},
	}}
	request := validHTTPAsterAgentApproval(now)
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	server.handleAsterAgentApprove(response, httptest.NewRequest(
		http.MethodPost, "/api/v1/live/agents/aster/approve", bytes.NewReader(body),
	))
	if response.Code != http.StatusNoContent || approver.request.AgentAddress != request.AgentAddress {
		t.Fatalf("status = %d, relayed = %+v, body = %s", response.Code, approver.request, response.Body.String())
	}
}

func validHTTPAsterAgentApproval(now time.Time) asterlive.ApproveAgentRequest {
	return asterlive.ApproveAgentRequest{
		User: "0x1111111111111111111111111111111111111111", Nonce: now.UnixMicro(),
		Signature: "0x" + strings.Repeat("1", 128) + "1b",
		AgentName: "OrbitalMarkets", AgentAddress: "0x2222222222222222222222222222222222222222",
		Expired: now.Add(7 * 24 * time.Hour).UnixMilli(), CanPerpTrade: true,
		AsterChain: "Mainnet", SignatureChainID: 56,
		Builder: "0xe625a2d279815749c647daed24df41bc8dd14bfe", MaxFeeRate: "0.0002",
		BuilderName: "OrbitalMarkets", BuilderNonce: now.UnixMicro() + 1,
		BuilderSignature: "0x" + strings.Repeat("2", 128) + "1c",
	}
}

func TestHandleAsterAgentApproveRejectsPrivateKeyFields(t *testing.T) {
	server := &Server{live: &LiveDeps{
		asterAgentApprover: &fakeAsterAgentApprover{},
		asterBuilder:       &asterlive.BuilderConfig{},
	}}
	response := httptest.NewRecorder()
	server.handleAsterAgentApprove(response, httptest.NewRequest(
		http.MethodPost, "/api/v1/live/agents/aster/approve",
		bytes.NewBufferString(`{"privateKey":"secret"}`),
	))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", response.Code)
	}
}

type fakeHyperliquidBuilderApprover struct {
	request hllive.ApproveBuilderFeeRequest
}

func (f *fakeHyperliquidBuilderApprover) ApproveBuilderFee(_ context.Context, request hllive.ApproveBuilderFeeRequest) error {
	f.request = request
	return nil
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

func TestHandleHyperliquidBuilderFeeApproveValidatesConfiguredBuilder(t *testing.T) {
	approver := &fakeHyperliquidBuilderApprover{}
	builder := &hllive.BuilderCode{Address: "0x1111111111111111111111111111111111111111", Fee: 20}
	server := &Server{live: &LiveDeps{hlBuilder: builder, hlBuilderApprover: approver}}
	request := signedHyperliquidBuilderApproval(t, time.Now().UnixMilli(), builder.Address)
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()

	server.handleHyperliquidBuilderFeeApprove(response, httptest.NewRequest(http.MethodPost, "/api/v1/live/agents/hyperliquid/approve-builder-fee", bytes.NewReader(body)))

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if approver.request.Action.MaxFeeRate != "0.02%" || approver.request.Action.Builder != builder.Address {
		t.Fatalf("relayed action = %+v", approver.request.Action)
	}
}

func signedHyperliquidBuilderApproval(t *testing.T, nonce int64, builder string) hllive.ApproveBuilderFeeRequest {
	t.Helper()
	request := hllive.ApproveBuilderFeeRequest{
		OwnerAddress: "0x14791697260E4c9A71f18484C9f997B308e59325",
		Action: hllive.ApproveBuilderFeeAction{
			Type: "approveBuilderFee", HyperliquidChain: "Mainnet", SignatureChainID: "0x1",
			MaxFeeRate: "0.02%", Builder: builder, Nonce: nonce,
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

func TestAgentChangeIsBlockedDuringActiveSession(t *testing.T) {
	server, _ := newResidualExposureServer(t)
	err := server.live.liveStore.UpsertDurableSession(context.Background(), executor.DurableSessionRecord{
		ID: "active-agent-session", State: "awaiting_leg2_sign", Payload: []byte(`{}`),
		AccountPacifica: "sol-owner", AccountHyperliquid: "0xowner", Asset: "SOL",
		HasExposure: true, ExpiresAt: time.Now().Add(time.Minute), CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	blocked, err := server.agentChangeBlocked(context.Background(), "hyperliquid", "0xOWNER")
	if err != nil {
		t.Fatal(err)
	}
	if !blocked {
		t.Fatal("agent change was allowed during an active session")
	}
}

type fakePacificaAgentBinder struct {
	request pacificlive.BindAgentRequest
}

type fakePacificaAgentRevoker struct {
	request pacificlive.RevokeAgentRequest
}

type fakePacificaBuilderApprover struct {
	request pacificlive.ApproveBuilderCodeRequest
}

type fakePacificaBuilderApprovalReader struct {
	account  string
	approved bool
}

func (f *fakePacificaBuilderApprovalReader) HasBuilderCodeApproval(_ context.Context, account string, _ pacificlive.BuilderConfig) (bool, error) {
	f.account = account
	return f.approved, nil
}

func TestHandlePacificaBuilderCodeApprovalReturnsCurrentStatus(t *testing.T) {
	reader := &fakePacificaBuilderApprovalReader{approved: true}
	server := &Server{live: &LiveDeps{
		pacificaBuilder: pacificlive.OrbitalBuilderConfig(), pacificaBuilderApprovalReader: reader,
	}}
	response := httptest.NewRecorder()

	server.handlePacificaBuilderCodeApproval(response, httptest.NewRequest(
		http.MethodGet, "/api/v1/live/agents/pacifica/builder-code-approval?account=sol-owner", nil,
	))

	if response.Code != http.StatusOK || reader.account != "sol-owner" || !bytes.Contains(response.Body.Bytes(), []byte(`"approved":true`)) {
		t.Fatalf("status = %d, account = %q, body = %s", response.Code, reader.account, response.Body.String())
	}
}

func (f *fakePacificaBuilderApprover) ApproveBuilderCode(_ context.Context, request pacificlive.ApproveBuilderCodeRequest) error {
	f.request = request
	return nil
}

func TestHandlePacificaBuilderCodeApproveValidatesAndRelays(t *testing.T) {
	approver := &fakePacificaBuilderApprover{}
	builder := pacificlive.OrbitalBuilderConfig()
	server := &Server{live: &LiveDeps{pacificaBuilder: builder, pacificaBuilderApprover: approver}}
	owner := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	timestamp := time.Now().UnixMilli()
	message, err := pacificlive.BuildSigningMessage("approve_builder_code", timestamp, 30_000, map[string]any{
		"builder_code": builder.Code,
		"max_fee_rate": builder.MaxFeeRate,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := pacificlive.ApproveBuilderCodeRequest{
		Account: ownerAddress(owner), Signature: base58.Encode(ed25519.Sign(owner, message)),
		Timestamp: timestamp, ExpiryWindow: 30_000, BuilderCode: builder.Code, MaxFeeRate: builder.MaxFeeRate,
	}
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()

	server.handlePacificaBuilderCodeApprove(response, httptest.NewRequest(http.MethodPost, "/api/v1/live/agents/pacifica/approve-builder-code", bytes.NewReader(body)))

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if approver.request.BuilderCode != "orbitalmarkets" || approver.request.MaxFeeRate != "0.0002" {
		t.Fatalf("relayed request = %+v", approver.request)
	}
}

func (f *fakePacificaAgentBinder) BindAgent(_ context.Context, request pacificlive.BindAgentRequest) error {
	f.request = request
	return nil
}

func (f *fakePacificaAgentRevoker) RevokeAgent(_ context.Context, request pacificlive.RevokeAgentRequest) error {
	f.request = request
	return nil
}

func TestHandlePacificaAgentRevokeVerifiesOwnerSignatureAndRelays(t *testing.T) {
	revoker := &fakePacificaAgentRevoker{}
	server := &Server{live: &LiveDeps{pacificaAgentRevoker: revoker}}
	request := validPacificaRevokeRequest(t, time.Now().UnixMilli())
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()

	server.handlePacificaAgentRevoke(response, httptest.NewRequest(http.MethodPost, "/api/v1/live/agents/pacifica/revoke", bytes.NewReader(body)))

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if revoker.request.AgentWallet != request.AgentWallet {
		t.Fatalf("relayed request = %+v", revoker.request)
	}
}

func TestHandlePacificaAgentRevokeRejectsWrongOwnerSignature(t *testing.T) {
	server := &Server{live: &LiveDeps{pacificaAgentRevoker: &fakePacificaAgentRevoker{}}}
	request := validPacificaRevokeRequest(t, time.Now().UnixMilli())
	request.Signature = base58.Encode(make([]byte, ed25519.SignatureSize))
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()

	server.handlePacificaAgentRevoke(response, httptest.NewRequest(http.MethodPost, "/api/v1/live/agents/pacifica/revoke", bytes.NewReader(body)))

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestHandlePacificaAgentRevokeIsBlockedDuringActiveSession(t *testing.T) {
	server, _ := newResidualExposureServer(t)
	revoker := &fakePacificaAgentRevoker{}
	server.live.pacificaAgentRevoker = revoker
	request := validPacificaRevokeRequest(t, time.Now().UnixMilli())
	err := server.live.liveStore.UpsertDurableSession(context.Background(), executor.DurableSessionRecord{
		ID: "active-pacifica-revoke", State: "awaiting_leg2_sign", Payload: []byte(`{}`),
		AccountPacifica: request.Account, AccountHyperliquid: "0xowner", Asset: "SOL",
		HasExposure: true, ExpiresAt: time.Now().Add(time.Minute), CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()

	server.handlePacificaAgentRevoke(response, httptest.NewRequest(http.MethodPost, "/api/v1/live/agents/pacifica/revoke", bytes.NewReader(body)))

	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if revoker.request.Account != "" {
		t.Fatal("active-session revocation reached Pacifica")
	}
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
	message, err := pacificlive.BuildSigningMessage("bind_agent_wallet", timestamp, 30_000, map[string]any{
		"agent_wallet": base58.Encode(agent.Public().(ed25519.PublicKey)),
	})
	if err != nil {
		t.Fatal(err)
	}
	return pacificlive.BindAgentRequest{
		Account: ownerAddress(owner), Signature: base58.Encode(ed25519.Sign(owner, message)),
		Timestamp: timestamp, ExpiryWindow: 30_000,
		AgentWallet: base58.Encode(agent.Public().(ed25519.PublicKey)),
	}
}

func validPacificaRevokeRequest(t *testing.T, timestamp int64) pacificlive.RevokeAgentRequest {
	t.Helper()
	owner := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	agentSeed := make([]byte, ed25519.SeedSize)
	for index := range agentSeed {
		agentSeed[index] = byte(index + 32)
	}
	agent := ed25519.NewKeyFromSeed(agentSeed)
	agentAddress := base58.Encode(agent.Public().(ed25519.PublicKey))
	message, err := pacificlive.BuildSigningMessage("revoke_agent_wallet", timestamp, 30_000, map[string]any{
		"agent_wallet": agentAddress,
	})
	if err != nil {
		t.Fatal(err)
	}
	return pacificlive.RevokeAgentRequest{
		Account: ownerAddress(owner), Signature: base58.Encode(ed25519.Sign(owner, message)),
		Timestamp: timestamp, ExpiryWindow: 30_000, AgentWallet: agentAddress,
	}
}

func ownerAddress(privateKey ed25519.PrivateKey) string {
	return base58.Encode(privateKey.Public().(ed25519.PublicKey))
}
