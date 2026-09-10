package dataagent

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const (
	testOwner                      = "0x1111111111111111111111111111111111111111"
	testDataAgent                  = "0x14791697260e4c9a71f18484c9f997b308e59325"
	testExecutionAgent             = "0x9999999999999999999999999999999999999999"
	testRotatedExecutionAgent      = "0x8888888888888888888888888888888888888888"
	testUnauthorizedExecutionAgent = "0x7777777777777777777777777777777777777777"
)

func TestClientSendsExactDeterministicSignedReadRequests(t *testing.T) {
	privateKey, _ := hex.DecodeString("0123456789012345678901234567890123456789012345678901234567890123")
	requests := make([]string, 0, 4)
	venue := newReadVenue(t, func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.RequestURI())
		writeSuccessfulReadResponse(w, r)
	})
	defer venue.Close()

	now := time.UnixMilli(1_700_000_000_000)
	client := NewClient(venue.URL, venue.Client(), func() time.Time { return now })
	agent := addressFromPrivateKey(privateKey)
	report := client.Probe(context.Background(), testOwner, agent, testExecutionAgent, privateKey, 1_731_536_000_000)
	if !report.Success || !report.ExecutionAgentPreserved {
		t.Fatalf("report = %+v", report)
	}
	want := []string{
		"/fapi/v3/agent?asterChain=Mainnet&user=" + testOwner + "&signer=" + agent + "&nonce=1700000000000000&signature=0x8d917a8df3f14719a783abe95559d75a20c70dd8c4d53aee6f7771f59b0201085ed5dd17ebd47b08e66de9ddc3cf59109ba96a21fcefab653aebbd4fecd991771b",
		"/fapi/v3/accountWithJoinMargin?asterChain=Mainnet&user=" + testOwner + "&signer=" + agent + "&nonce=1700000000000001&signature=0x5ec8a67a6c6fb3b863d7c202405440b0bfa0fcd7560d16edaf3e24a459f0bea406e75501e1218ebce552a1bd4cd448bbe1b86a11c3225a0f43a78fcc61f74e1d1b",
		"/fapi/v3/positionRisk?asterChain=Mainnet&user=" + testOwner + "&signer=" + agent + "&nonce=1700000000000002&signature=0x3b0de5e84184dea02809491795ac63c2d85b31cc3bebad115e0b45518c9e274f737734050b92f2e5f80f1019af91410dd7ae6ae9d61e026f63c749130759fbaf1b",
		"/fapi/v3/income?incomeType=FUNDING_FEE&startTime=1699395200000&endTime=1700000000000&limit=100&asterChain=Mainnet&user=" + testOwner + "&signer=" + agent + "&nonce=1700000000000003&signature=0x795ddb19f0ad80956f3daff273334a8d98d21b259ecf91b324b5321d20b9257004db13edd8d591a6e8ab24b671970549aecd565963a93ac7ed3fa84879d7c7d71c",
	}
	if len(requests) != len(want) {
		t.Fatalf("request count = %d", len(requests))
	}
	for index := range requests {
		if requests[index] != want[index] {
			t.Fatalf("request %d = %s, want %s", index, requests[index], want[index])
		}
	}
}

func TestClientRequiresExecutionAgentInVenueResponse(t *testing.T) {
	privateKey, _ := hex.DecodeString("0123456789012345678901234567890123456789012345678901234567890123")
	venue := newReadVenue(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/fapi/v3/agent" {
			fmt.Fprint(w, `[{"agentAddress":"`+testDataAgent+`","canRead":true,"canSpotTrade":false,"canPerpTrade":false,"canWithdraw":false,"expired":1731536000000}]`)
			return
		}
		writeSuccessfulReadResponse(w, r)
	})
	defer venue.Close()
	client := NewClient(venue.URL, venue.Client(), func() time.Time { return time.UnixMilli(1_700_000_000_000) })
	report := client.Probe(context.Background(), testOwner, testDataAgent, testExecutionAgent, privateKey, 1_731_536_000_000)
	if report.Success || report.ExecutionAgentPreserved || report.Error != "expected Aster execution agent is missing" {
		t.Fatalf("report = %+v", report)
	}
}

func TestClientRequiresUsableExecutionAgentPermissions(t *testing.T) {
	privateKey, _ := hex.DecodeString("0123456789012345678901234567890123456789012345678901234567890123")
	venue := newReadVenue(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/fapi/v3/agent" {
			fmt.Fprint(w, `[{"agentAddress":"`+testDataAgent+`","canRead":true,"canSpotTrade":false,"canPerpTrade":false,"canWithdraw":false,"expired":1731536000000},{"agentAddress":"`+testExecutionAgent+`","canRead":true,"canSpotTrade":false,"canPerpTrade":false,"canWithdraw":false,"expired":1731536000000}]`)
			return
		}
		writeSuccessfulReadResponse(w, r)
	})
	defer venue.Close()
	client := NewClient(venue.URL, venue.Client(), func() time.Time { return time.UnixMilli(1_700_000_000_000) })
	report := client.Probe(context.Background(), testOwner, testDataAgent, testExecutionAgent, privateKey, 1_731_536_000_000)
	if report.Success || report.ExecutionAgentPreserved {
		t.Fatalf("report = %+v", report)
	}
}

func TestClientRejectsEmptyAccountAndStringVenueError(t *testing.T) {
	privateKey, _ := hex.DecodeString("0123456789012345678901234567890123456789012345678901234567890123")
	for _, body := range []string{`{}`, `{"code":"-2015","msg":"rejected"}`} {
		venue := newReadVenue(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/fapi/v3/accountWithJoinMargin" {
				fmt.Fprint(w, body)
				return
			}
			writeSuccessfulReadResponse(w, r)
		})
		client := NewClient(venue.URL, venue.Client(), func() time.Time { return time.UnixMilli(1_700_000_000_000) })
		report := client.Probe(context.Background(), testOwner, testDataAgent, testExecutionAgent, privateKey, 1_731_536_000_000)
		venue.Close()
		if report.Success || report.Endpoints.Account {
			t.Fatalf("body %s report = %+v", body, report)
		}
	}
}

func TestClientRejectsUnallowlistedEndpointsInvalidRedirectAndOversizedResponses(t *testing.T) {
	privateKey, _ := hex.DecodeString("0123456789012345678901234567890123456789012345678901234567890123")
	client := NewClient("http://example.invalid", nil, time.Now)
	if _, err := client.signedGET(context.Background(), "/fapi/v3/leverage", nil, testOwner, testDataAgent, privateKey, 1); err == nil {
		t.Fatal("unallowlisted endpoint was sent")
	}
	for _, request := range []struct {
		path   string
		params []pair
	}{
		{"/fapi/v3/positionSide/dual", []pair{{"symbol", "BTCUSDT"}}},
		{"/fapi/v3/leverageBracket", nil},
		{"/fapi/v3/order", []pair{{"symbol", "BTCUSDT"}, {"orderId", "123"}}},
		{"/fapi/v3/income", []pair{{"incomeType", "FUNDING_FEE"}, {"startTime", "later"}, {"endTime", "earlier"}, {"limit", "100"}}},
	} {
		if _, err := client.signedGET(context.Background(), request.path, request.params, testOwner, testDataAgent, privateKey, 1); err == nil {
			t.Fatalf("unexpected parameters allowed for %s", request.path)
		}
	}
	tests := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{"invalid JSON", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, `not-json`) }},
		{"redirect", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/elsewhere", http.StatusFound) }},
		{"oversized", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(strings.Repeat("x", maxReadResponse+1)))
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			venue := httptest.NewServer(test.handler)
			defer venue.Close()
			client := NewClient(venue.URL, venue.Client(), func() time.Time { return time.UnixMilli(1_700_000_000_000) })
			report := client.Probe(context.Background(), testOwner, testDataAgent, testExecutionAgent, privateKey, 1_731_536_000_000)
			if report.Success || report.Error == "" {
				t.Fatalf("report = %+v", report)
			}
		})
	}
}

func TestClientChecksRemainingCapabilitiesWithExactRequests(t *testing.T) {
	privateKey, _ := hex.DecodeString("0123456789012345678901234567890123456789012345678901234567890123")
	requests := make([]string, 0, 3)
	venue := newReadVenue(t, func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.RequestURI())
		writeSuccessfulReadResponse(w, r)
	})
	defer venue.Close()

	client := NewClient(venue.URL, venue.Client(), func() time.Time { return time.UnixMilli(1_700_000_000_000) })
	calls := []struct {
		path   string
		params []pair
		valid  func([]byte) error
	}{
		{"/fapi/v3/positionSide/dual", nil, validatePositionMode},
		{"/fapi/v3/leverageBracket", []pair{{"symbol", "BTCUSDT"}}, func(body []byte) error {
			return validateLeverageBrackets(body, "BTCUSDT")
		}},
		{"/fapi/v3/order", []pair{{"symbol", "BTCUSDT"}, {"origClientOrderId", "orbital-order-1"}}, func(body []byte) error {
			return validateOrder(body, "BTCUSDT", "orbital-order-1")
		}},
	}
	for _, call := range calls {
		body, err := client.signedGET(context.Background(), call.path, call.params, testOwner, testDataAgent, privateKey, client.nextNonce())
		if err != nil {
			t.Fatalf("%s request failed: %v", call.path, err)
		}
		if err := call.valid(body); err != nil {
			t.Fatalf("%s response failed: %v", call.path, err)
		}
	}
	wantPrefixes := []string{
		"/fapi/v3/positionSide/dual?asterChain=Mainnet&user=" + testOwner + "&signer=" + testDataAgent + "&nonce=1700000000000000&signature=0x",
		"/fapi/v3/leverageBracket?symbol=BTCUSDT&asterChain=Mainnet&user=" + testOwner + "&signer=" + testDataAgent + "&nonce=1700000000000001&signature=0x",
		"/fapi/v3/order?symbol=BTCUSDT&origClientOrderId=orbital-order-1&asterChain=Mainnet&user=" + testOwner + "&signer=" + testDataAgent + "&nonce=1700000000000002&signature=0x",
	}
	if len(requests) != len(wantPrefixes) {
		t.Fatalf("request count = %d", len(requests))
	}
	for index := range requests {
		if !strings.HasPrefix(requests[index], wantPrefixes[index]) || len(requests[index]) != len(wantPrefixes[index])+130 {
			t.Fatalf("request %d = %s", index, requests[index])
		}
	}
}

func TestClientRejectsInvalidCapabilityResponses(t *testing.T) {
	tests := []struct {
		name  string
		body  string
		valid func([]byte) error
	}{
		{"position mode", `{}`, validatePositionMode},
		{"leverage correlation", `{"symbol":"ETHUSDT","brackets":[{"initialLeverage":5,"notionalCap":100000,"notionalFloor":0}]}`, func(body []byte) error {
			return validateLeverageBrackets(body, "BTCUSDT")
		}},
		{"order correlation", `{"orderId":123,"clientOrderId":"other-order","symbol":"BTCUSDT","status":"FILLED","executedQty":"1","avgPrice":"100"}`, func(body []byte) error {
			return validateOrder(body, "BTCUSDT", "orbital-order-1")
		}},
		{"order id", `{"orderId":"bad","clientOrderId":"orbital-order-1","symbol":"BTCUSDT","status":"FILLED","executedQty":"1","avgPrice":"100"}`, func(body []byte) error {
			return validateOrder(body, "BTCUSDT", "orbital-order-1")
		}},
		{"order status", `{"orderId":123,"clientOrderId":"orbital-order-1","symbol":"BTCUSDT","status":"UNKNOWN","executedQty":"1","avgPrice":"100"}`, func(body []byte) error {
			return validateOrder(body, "BTCUSDT", "orbital-order-1")
		}},
		{"order quantity", `{"orderId":123,"clientOrderId":"orbital-order-1","symbol":"BTCUSDT","status":"FILLED","executedQty":"NaN","avgPrice":"100"}`, func(body []byte) error {
			return validateOrder(body, "BTCUSDT", "orbital-order-1")
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.valid([]byte(test.body)) == nil {
				t.Fatal("invalid capability response accepted")
			}
		})
	}
}

func TestClientClassifiesApprovalOutcomes(t *testing.T) {
	approval := Approval{User: testOwner, AgentAddress: testDataAgent, AgentName: agentName, AsterChain: "Mainnet", SignatureChainID: 56}
	tests := []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"explicit rejection", http.StatusBadRequest, `{"code":-1,"msg":"rejected"}`, ErrApprovalRejected},
		{"server ambiguity", http.StatusInternalServerError, `{"code":-1}`, ErrApprovalAmbiguous},
		{"invalid success response", http.StatusOK, `not-json`, ErrApprovalAmbiguous},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			venue := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				fmt.Fprint(w, test.body)
			}))
			defer venue.Close()
			client := NewClient(venue.URL, venue.Client(), time.Now)
			if err := client.Approve(context.Background(), approval, testSignature); !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := NewClient("", nil, time.Now).Approve(canceled, approval, testSignature); !errors.Is(err, ErrApprovalNotSent) {
		t.Fatalf("canceled approval error = %v", err)
	}
}

func newReadVenue(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	return httptest.NewServer(handler)
}

func writeSuccessfulReadResponse(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/fapi/v3/agent":
		fmt.Fprint(w, `[{"agentAddress":"`+testDataAgent+`","canRead":true,"canSpotTrade":false,"canPerpTrade":false,"canWithdraw":false,"expired":1731536000000},{"agentAddress":"`+testExecutionAgent+`","canRead":true,"canSpotTrade":false,"canPerpTrade":true,"canWithdraw":false,"expired":1731536000000}]`)
	case "/fapi/v3/accountWithJoinMargin":
		fmt.Fprint(w, `{"canTrade":true,"totalMarginBalance":"123.5","availableBalance":"100.25","positions":[]}`)
	case "/fapi/v3/positionRisk", "/fapi/v3/income":
		fmt.Fprint(w, `[]`)
	case "/fapi/v3/positionSide/dual":
		fmt.Fprint(w, `{"dualSidePosition":false}`)
	case "/fapi/v3/leverageBracket":
		fmt.Fprint(w, `{"symbol":"BTCUSDT","brackets":[{"initialLeverage":5,"notionalCap":100000,"notionalFloor":0}]}`)
	case "/fapi/v3/order":
		fmt.Fprint(w, `{"orderId":123,"clientOrderId":"orbital-order-1","symbol":"BTCUSDT","status":"FILLED","executedQty":"1","avgPrice":"100"}`)
	default:
		http.Error(w, "unexpected", http.StatusNotFound)
	}
}
