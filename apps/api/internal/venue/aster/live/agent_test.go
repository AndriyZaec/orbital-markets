package live

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestApproveAgentRequestAllowsOnlyBoundedPerpetualAccess(t *testing.T) {
	now := time.Now()
	request := validApproveAgentRequest(now)
	if err := request.Validate(now, testAsterBuilder()); err != nil {
		t.Fatal(err)
	}
	request.CanWithdraw = true
	if err := request.Validate(now, testAsterBuilder()); err == nil {
		t.Fatal("withdraw-enabled Aster agent was accepted")
	}
	request = validApproveAgentRequest(now)
	request.MaxFeeRate = "0.001"
	if err := request.Validate(now, testAsterBuilder()); err == nil {
		t.Fatal("altered Aster builder fee cap was accepted")
	}
}

func TestAgentApproverRelaysWithoutPrivateKey(t *testing.T) {
	relayed := make(map[string]string)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		relayed[request.URL.Path] = string(body)
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"code":200,"msg":"success"}`))
	}))
	defer server.Close()

	approver := NewAgentApprover(server.URL+"/agent", server.URL+"/builder", server.Client())
	if err := approver.ApproveAgent(context.Background(), validApproveAgentRequest(time.Now())); err != nil {
		t.Fatal(err)
	}
	agentBody := relayed["/agent"]
	builderBody := relayed["/builder"]
	if strings.Contains(strings.ToLower(agentBody+builderBody), "private") ||
		!strings.HasPrefix(agentBody, "agentName=OrbitalMarkets&agentAddress=0x2222222222222222222222222222222222222222&ipWhitelist=&expired=") ||
		!strings.Contains(agentBody, "&canSpotTrade=false&canPerpTrade=true&canWithdraw=false&asterChain=Mainnet&user=0x1111111111111111111111111111111111111111&nonce=") ||
		!strings.Contains(agentBody, "&signatureChainId=56&signature=0x") ||
		!strings.HasPrefix(builderBody, "builder=0xe625a2d279815749c647daed24df41bc8dd14bfe&maxFeeRate=0.0002&builderName=OrbitalMarkets&asterChain=Mainnet&user=0x1111111111111111111111111111111111111111&nonce=") ||
		!strings.Contains(builderBody, "&signatureChainId=56&signature=0x") {
		t.Fatalf("agent body = %s, builder body = %s", agentBody, builderBody)
	}
}

func TestAgentApproverTreatsAgentServerFailureAsAmbiguous(t *testing.T) {
	for _, status := range []int{http.StatusRequestTimeout, http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				if request.URL.Path == "/agent" {
					response.WriteHeader(status)
					return
				}
				_, _ = response.Write([]byte(`{"code":200,"msg":"success"}`))
			}))
			defer server.Close()

			err := NewAgentApprover(server.URL+"/agent", server.URL+"/builder", server.Client()).
				ApproveAgent(context.Background(), validApproveAgentRequest(time.Now()))
			if !errors.Is(err, ErrSubmissionAmbiguous) {
				t.Fatalf("error = %v, want ambiguous approval outcome", err)
			}
		})
	}
}

func TestAgentApproverTreatsConnectionDropAfterAgentConsumptionAsAmbiguous(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/builder" {
			_, _ = response.Write([]byte(`{"code":200,"msg":"success"}`))
			return
		}
		if _, err := io.ReadAll(request.Body); err != nil {
			t.Errorf("read consumed agent approval: %v", err)
			return
		}
		connection, _, err := response.(http.Hijacker).Hijack()
		if err != nil {
			t.Errorf("hijack agent approval connection: %v", err)
			return
		}
		_ = connection.Close()
	}))
	defer server.Close()

	err := NewAgentApprover(server.URL+"/agent", server.URL+"/builder", server.Client()).
		ApproveAgent(context.Background(), validApproveAgentRequest(time.Now()))
	if !errors.Is(err, ErrSubmissionAmbiguous) {
		t.Fatalf("error = %v, want ambiguous approval outcome", err)
	}
}

func TestAgentApproverTreatsOversizedAgentSuccessAsAmbiguous(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		body := `{"code":200,"msg":"success"}`
		if request.URL.Path == "/agent" {
			body += strings.Repeat(" ", maxApprovalResponse)
		}
		_, _ = response.Write([]byte(body))
	}))
	defer server.Close()

	err := NewAgentApprover(server.URL+"/agent", server.URL+"/builder", server.Client()).
		ApproveAgent(context.Background(), validApproveAgentRequest(time.Now()))
	if !errors.Is(err, ErrSubmissionAmbiguous) {
		t.Fatalf("error = %v, want ambiguous approval outcome", err)
	}
}

func TestAgentApproverTreatsAmbiguousBuilderApprovalAsAgentNotSent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/builder" {
			response.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		t.Fatal("execution-agent approval must not be sent after an ambiguous builder approval")
	}))
	defer server.Close()

	err := NewAgentApprover(server.URL+"/agent", server.URL+"/builder", server.Client()).
		ApproveAgent(context.Background(), validApproveAgentRequest(time.Now()))
	if !errors.Is(err, ErrSubmissionNotSent) {
		t.Fatalf("error = %v, want execution-agent submission not sent", err)
	}
}

func validApproveAgentRequest(now time.Time) ApproveAgentRequest {
	return ApproveAgentRequest{
		User:             "0x1111111111111111111111111111111111111111",
		Nonce:            now.UnixMicro(),
		Signature:        "0x" + strings.Repeat("1", 128) + "1b",
		AgentName:        asterAgentName,
		AgentAddress:     "0x2222222222222222222222222222222222222222",
		Expired:          now.Add(7 * 24 * time.Hour).UnixMilli(),
		CanPerpTrade:     true,
		AsterChain:       mainnetName,
		SignatureChainID: asterSignatureChainID,
		Builder:          "0xe625a2d279815749c647daed24df41bc8dd14bfe",
		MaxFeeRate:       "0.0002",
		BuilderName:      asterAgentName,
		BuilderNonce:     now.UnixMicro() + 1,
		BuilderSignature: "0x" + strings.Repeat("2", 128) + "1c",
	}
}

func testAsterBuilder() BuilderConfig {
	return BuilderConfig{Address: "0xe625a2d279815749c647daed24df41bc8dd14bfe", FeeRate: "0.0002"}
}
