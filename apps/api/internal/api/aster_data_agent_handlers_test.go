package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/aster/dataagent"
)

type fakeAsterDataAgentProbe struct {
	prepared  dataagent.Prepared
	status    dataagent.ProbeStatus
	report    dataagent.Report
	err       error
	account   string
	executor  string
	probeID   string
	signature string
}

func (f *fakeAsterDataAgentProbe) Prepare(_ context.Context, account, executionAgent string) (dataagent.Prepared, error) {
	f.account, f.executor = account, executionAgent
	return f.prepared, f.err
}
func (f *fakeAsterDataAgentProbe) Validate(_ context.Context, probeID, signature, account, executionAgent string) (dataagent.Report, error) {
	f.probeID, f.signature, f.account, f.executor = probeID, signature, account, executionAgent
	return f.report, f.err
}
func (f *fakeAsterDataAgentProbe) Status(_ context.Context, account, executionAgent string) (dataagent.ProbeStatus, error) {
	f.account, f.executor = account, executionAgent
	return f.status, f.err
}
func (f *fakeAsterDataAgentProbe) Run(_ context.Context, account, executionAgent string) (dataagent.Report, error) {
	f.account, f.executor = account, executionAgent
	return f.report, f.err
}

func TestAsterDataAgentPrepareContract(t *testing.T) {
	fake := &fakeAsterDataAgentProbe{prepared: dataagent.Prepared{ProbeID: "probe-1", Approval: dataagent.Approval{
		User: testHTTPAsterOwner, Nonce: 1, AgentName: "OrbitalData", AgentAddress: testHTTPDataAgent,
		Expired: 2, AsterChain: "Mainnet", SignatureChainID: 56,
	}}}
	response := serveDataAgentRequest(t, fake, http.MethodPost, "/api/v1/live/aster/data-agent/prepare",
		`{"account":"`+testHTTPAsterOwner+`","execution_agent":"`+testHTTPExecutionAgent+`"}`)
	if response.Code != http.StatusOK || fake.account == "" || fake.executor == "" {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	assertNoSensitiveFields(t, response.Body.Bytes())
}

func TestAsterDataAgentValidateReturnsCapabilityReport(t *testing.T) {
	fake := &fakeAsterDataAgentProbe{report: dataagent.Report{
		Endpoints: dataagent.EndpointReport{Agent: true}, RequestedExpiry: 2,
		ExecutionAgentPreserved: false, Error: "expected Aster execution agent is missing",
	}}
	response := serveDataAgentRequest(t, fake, http.MethodPost, "/api/v1/live/aster/data-agent/validate",
		`{"probe_id":"probe-1","signature":"`+testHTTPSignature+`","account":"`+testHTTPAsterOwner+`","execution_agent":"`+testHTTPExecutionAgent+`"}`)
	if response.Code != http.StatusOK || fake.probeID != "probe-1" || fake.signature == "" || fake.account == "" || fake.executor == "" {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	assertNoSensitiveFields(t, response.Body.Bytes())
}

func TestAsterDataAgentStatusAndRunContracts(t *testing.T) {
	fake := &fakeAsterDataAgentProbe{status: dataagent.ProbeStatus{
		Status: dataagent.StatusApproved, AgentAddress: testHTTPDataAgent, RequestedExpiry: 2,
		LastError: "Aster account response was invalid",
	}, report: dataagent.Report{RequestedExpiry: 2, Error: "Aster account response was invalid"}}
	status := serveDataAgentRequest(t, fake, http.MethodGet,
		"/api/v1/live/aster/data-agent/status?account="+testHTTPAsterOwner+"&execution_agent="+testHTTPExecutionAgent, "")
	if status.Code != http.StatusOK || fake.account != testHTTPAsterOwner || fake.executor != testHTTPExecutionAgent {
		t.Fatalf("status response = %d %s", status.Code, status.Body.String())
	}
	assertNoSensitiveFields(t, status.Body.Bytes())
	run := serveDataAgentRequest(t, fake, http.MethodPost, "/api/v1/live/aster/data-agent/run",
		`{"account":"`+testHTTPAsterOwner+`","execution_agent":"`+testHTTPExecutionAgent+`"}`)
	if run.Code != http.StatusOK {
		t.Fatalf("run response = %d %s", run.Code, run.Body.String())
	}
	assertNoSensitiveFields(t, run.Body.Bytes())
}

func TestAsterDataAgentReturnsSanitizedActionableErrors(t *testing.T) {
	fake := &fakeAsterDataAgentProbe{err: dataagent.ErrUncertain}
	response := serveDataAgentRequest(t, fake, http.MethodPost, "/api/v1/live/aster/data-agent/validate",
		`{"probe_id":"probe-1","signature":"`+testHTTPSignature+`","account":"`+testHTTPAsterOwner+`","execution_agent":"`+testHTTPExecutionAgent+`"}`)
	if response.Code != http.StatusConflict || !bytes.Contains(response.Body.Bytes(), []byte("uncertain")) || bytes.Contains(response.Body.Bytes(), []byte("signature")) {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestAsterDataAgentEndpointsUnavailableWithoutMasterKey(t *testing.T) {
	server := &Server{}
	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodPost, "/api/v1/live/aster/data-agent/prepare", bytes.NewBufferString(`{}`)),
		httptest.NewRequest(http.MethodPost, "/api/v1/live/aster/data-agent/validate", bytes.NewBufferString(`{}`)),
		httptest.NewRequest(http.MethodGet, "/api/v1/live/aster/data-agent/status", nil),
		httptest.NewRequest(http.MethodPost, "/api/v1/live/aster/data-agent/run", bytes.NewBufferString(`{}`)),
	} {
		response := httptest.NewRecorder()
		switch {
		case request.URL.Path[len(request.URL.Path)-6:] == "status":
			server.handleAsterDataAgentStatus(response, request)
		case request.URL.Path[len(request.URL.Path)-3:] == "run":
			server.handleAsterDataAgentRun(response, request)
		case request.URL.Path[len(request.URL.Path)-7:] == "prepare":
			server.handleAsterDataAgentPrepare(response, request)
		default:
			server.handleAsterDataAgentValidate(response, request)
		}
		if response.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s status = %d", request.URL.Path, response.Code)
		}
	}
}

func serveDataAgentRequest(t *testing.T, fake *fakeAsterDataAgentProbe, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	server := &Server{asterDataAgent: fake, live: &LiveDeps{}}
	request := httptest.NewRequest(method, target, bytes.NewBufferString(body))
	response := httptest.NewRecorder()
	switch {
	case method == http.MethodGet:
		server.handleAsterDataAgentStatus(response, request)
	case target == "/api/v1/live/aster/data-agent/prepare":
		server.handleAsterDataAgentPrepare(response, request)
	case target == "/api/v1/live/aster/data-agent/validate":
		server.handleAsterDataAgentValidate(response, request)
	default:
		server.handleAsterDataAgentRun(response, request)
	}
	return response
}

func assertNoSensitiveFields(t *testing.T, body []byte) {
	t.Helper()
	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range [][]byte{[]byte(`"private`), []byte(`"signature":`), []byte("ciphertext"), []byte("key_nonce")} {
		if bytes.Contains(bytes.ToLower(body), forbidden) {
			t.Fatalf("sensitive response = %s", body)
		}
	}
}

const (
	testHTTPAsterOwner     = "0x1111111111111111111111111111111111111111"
	testHTTPDataAgent      = "0x2222222222222222222222222222222222222222"
	testHTTPExecutionAgent = "0x9999999999999999999999999999999999999999"
	testHTTPSignature      = "0x111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111b"
)
