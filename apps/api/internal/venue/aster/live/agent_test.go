package live

import (
	"context"
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
	var relayed string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		relayed = string(body)
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"code":200,"msg":"success"}`))
	}))
	defer server.Close()

	approver := NewAgentApprover(server.URL, server.Client())
	if err := approver.ApproveAgent(context.Background(), validApproveAgentRequest(time.Now())); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(relayed), "private") || !strings.Contains(relayed, "canPerpTrade=true") ||
		!strings.Contains(relayed, "builder=0x3333333333333333333333333333333333333333") ||
		!strings.Contains(relayed, "maxFeeRate=0.0002") {
		t.Fatalf("relayed body = %s", relayed)
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
		Builder:          testAsterBuilder().Address,
		MaxFeeRate:       testAsterBuilder().FeeRate,
		BuilderName:      asterAgentName,
		AsterChain:       mainnetName,
		SignatureChainID: asterSignatureChainID,
	}
}

func testAsterBuilder() BuilderConfig {
	return BuilderConfig{Address: "0x3333333333333333333333333333333333333333", FeeRate: "0.0002"}
}
