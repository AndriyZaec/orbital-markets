package live

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
)

func TestSubmitSignedOrderRelaysExactQueryAndParsesResult(t *testing.T) {
	request, signed := validSignedOrder(t)
	var received string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, httpRequest *http.Request) {
		if got := httpRequest.Header.Get("Content-Type"); got != "application/x-www-form-urlencoded" {
			t.Errorf("content type = %q", got)
		}
		body, err := io.ReadAll(httpRequest.Body)
		if err != nil {
			t.Fatal(err)
		}
		received = string(body)
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"orderId":1234567890123456789,"clientOrderId":"orbital-order-1","symbol":"BTCUSDT","status":"FILLED","executedQty":"1","cumQuote":"100.5","avgPrice":"100.5","origQty":"1","price":"100.5","side":"BUY","positionSide":"BOTH","timeInForce":"IOC","type":"LIMIT","reduceOnly":false}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, server.Client(), nil)
	result, err := client.SubmitSignedOrder(context.Background(), signed, request)
	if err != nil {
		t.Fatal(err)
	}
	var unsigned AsterUnsignedOrder
	if err := json.Unmarshal(request.UnsignedPayload, &unsigned); err != nil {
		t.Fatal(err)
	}
	want := unsigned.Message.Msg + "&signature=" + signed.Signature
	if received != want {
		t.Fatalf("body = %q, want %q", received, want)
	}
	if !result.Accepted || result.OrderID != "1234567890123456789" || result.ClientOrderID != request.ClientOrderID {
		t.Fatalf("result = %+v", result)
	}
	fill, err := client.WaitForFill(context.Background(), request.Account, request.ClientOrderID)
	if err != nil || !fill.Filled || fill.FilledAmount != 1 || fill.AvgFillPrice != 100.5 {
		t.Fatalf("fill = %+v, error = %v", fill, err)
	}
}

func TestSubmitSignedOrderRetainsPartialFillFromExpiredIOC(t *testing.T) {
	request, signed := validSignedOrder(t)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte(`{"orderId":42,"clientOrderId":"orbital-order-1","symbol":"BTCUSDT","status":"EXPIRED","executedQty":"0.4","cumQuote":"40.2","avgPrice":"100.5","origQty":"1","price":"100.5","side":"BUY","positionSide":"BOTH","timeInForce":"IOC","type":"LIMIT","reduceOnly":false}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, server.Client(), nil)
	result, err := client.SubmitSignedOrder(context.Background(), signed, request)
	if err != nil || !result.Accepted {
		t.Fatalf("result = %+v, error = %v", result, err)
	}
	fill, err := client.WaitForFill(context.Background(), request.Account, request.ClientOrderID)
	if err != nil {
		t.Fatal(err)
	}
	if !fill.Filled || fill.Status != "partial_fill" || fill.FilledAmount != 0.4 || fill.AvgFillPrice != 100.5 {
		t.Fatalf("fill = %+v", fill)
	}
}

func TestSubmitSignedOrderReturnsKnownVenueRejection(t *testing.T) {
	request, signed := validSignedOrder(t)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusBadRequest)
		_, _ = response.Write([]byte(`{"code":-1121,"msg":"Invalid symbol."}`))
	}))
	defer server.Close()

	result, err := NewClient(server.URL, server.Client(), nil).SubmitSignedOrder(context.Background(), signed, request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Accepted || result.Error != "Aster error -1121: Invalid symbol." {
		t.Fatalf("result = %+v", result)
	}
}

func TestSubmitSignedOrderTreatsServerFailureAsAmbiguous(t *testing.T) {
	request, signed := validSignedOrder(t)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusServiceUnavailable)
		_, _ = response.Write([]byte(`{"code":-1007,"msg":"Timeout waiting for response"}`))
	}))
	defer server.Close()

	_, err := NewClient(server.URL, server.Client(), nil).SubmitSignedOrder(context.Background(), signed, request)
	if !errors.Is(err, ErrSubmissionAmbiguous) {
		t.Fatalf("error = %v", err)
	}
}

func TestSubmitSignedOrderTreatsMalformedSuccessAsAmbiguous(t *testing.T) {
	request, signed := validSignedOrder(t)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte(`{"code":200,"msg":"success"}`))
	}))
	defer server.Close()

	_, err := NewClient(server.URL, server.Client(), nil).SubmitSignedOrder(context.Background(), signed, request)
	if !errors.Is(err, ErrSubmissionAmbiguous) {
		t.Fatalf("error = %v", err)
	}
}

func TestSubmitSignedOrderRejectsEndpointMetadataBeforeSending(t *testing.T) {
	request, signed := validSignedOrder(t)
	request.VenueMetadata = []byte(`{"order_url":"https://example.com/order"}`)
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	defer server.Close()

	if _, err := NewClient(server.URL, server.Client(), nil).SubmitSignedOrder(context.Background(), signed, request); !errors.Is(err, ErrSubmissionNotSent) {
		t.Fatal("tampered endpoint metadata was accepted")
	}
	if called {
		t.Fatal("request was sent")
	}
}

func TestValidateSignedOrderAcceptsBuilderAttribution(t *testing.T) {
	request, err := buildPayload(
		payloadTestRules, testUser, testSigner, "BTCUSDT", domain.SideLong,
		1, 100, "orbital-order-1", false, "open", openSlippageBPS,
		&BuilderConfig{Address: testBuilder, FeeRate: "0.0002"},
		time.Now(), time.Now().UnixMicro(),
	)
	if err != nil {
		t.Fatal(err)
	}
	signed := domain.SignedAction{
		RequestID: request.ID, ClientOrderID: request.ClientOrderID, Venue: "aster",
		SignerAddress: strings.ToUpper(testSigner[:2]) + testSigner[2:],
		Signature:     "0x" + strings.Repeat("1", 128) + "1b",
	}

	if _, err := validateSignedOrder(signed, request); err != nil {
		t.Fatalf("builder-attributed order rejected: %v", err)
	}
}

func TestSubmitSignedOrderRejectsMutatedQueryBeforeSending(t *testing.T) {
	request, signed := validSignedOrder(t)
	var unsigned AsterUnsignedOrder
	if err := json.Unmarshal(request.UnsignedPayload, &unsigned); err != nil {
		t.Fatal(err)
	}
	unsigned.Message.Msg += "&unexpected=value"
	request.UnsignedPayload, _ = json.Marshal(unsigned)
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	defer server.Close()

	if _, err := NewClient(server.URL, server.Client(), nil).SubmitSignedOrder(context.Background(), signed, request); !errors.Is(err, ErrSubmissionNotSent) {
		t.Fatal("mutated query was accepted")
	}
	if called {
		t.Fatal("request was sent")
	}
}

func validSignedOrder(t *testing.T) (*domain.SigningRequest, domain.SignedAction) {
	t.Helper()
	request, err := buildPayload(
		payloadTestRules, testUser, testSigner, "BTCUSDT", domain.SideLong,
		1, 100, "orbital-order-1", false, "open", openSlippageBPS, nil,
		time.Now(), time.Now().UnixMicro(),
	)
	if err != nil {
		t.Fatal(err)
	}
	return request, domain.SignedAction{
		RequestID: request.ID, ClientOrderID: request.ClientOrderID, Venue: "aster",
		SignerAddress: strings.ToUpper(testSigner[:2]) + testSigner[2:],
		Signature:     "0x" + strings.Repeat("1", 128) + "1b",
	}
}
