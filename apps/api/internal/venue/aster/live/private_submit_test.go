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

func TestSubmitSignedPrivateUsesQueryForReads(t *testing.T) {
	request, signed := validSignedPrivate(t, PrivateRequestParams{Operation: GetPositionMode})
	var receivedQuery, receivedBody string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, httpRequest *http.Request) {
		if httpRequest.Method != http.MethodGet || httpRequest.URL.Path != "/fapi/v3/positionSide/dual" {
			t.Errorf("request = %s %s", httpRequest.Method, httpRequest.URL.Path)
		}
		receivedQuery = httpRequest.URL.RawQuery
		body, _ := io.ReadAll(httpRequest.Body)
		receivedBody = string(body)
		_, _ = response.Write([]byte(`{"dualSidePosition":false}`))
	}))
	defer server.Close()

	result, err := NewClient(server.URL, server.Client(), nil).SubmitSignedPrivate(context.Background(), signed, request)
	if err != nil {
		t.Fatal(err)
	}
	if receivedQuery != signedPrivateWire(t, request, signed) || receivedBody != "" || result.Operation != GetPositionMode {
		t.Fatalf("query = %q, body = %q, result = %+v", receivedQuery, receivedBody, result)
	}
}

func TestSubmitSignedPrivateUsesFormBodyForMutations(t *testing.T) {
	request, signed := validSignedPrivate(t, PrivateRequestParams{Operation: UpdateLeverage, Symbol: "BTCUSDT", Leverage: 5})
	var received string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, httpRequest *http.Request) {
		if httpRequest.Method != http.MethodPost || httpRequest.URL.Path != "/fapi/v3/leverage" {
			t.Errorf("request = %s %s", httpRequest.Method, httpRequest.URL.Path)
		}
		body, _ := io.ReadAll(httpRequest.Body)
		received = string(body)
		_, _ = response.Write([]byte(`{"leverage":5,"maxNotionalValue":"1000000","symbol":"BTCUSDT"}`))
	}))
	defer server.Close()

	if _, err := NewClient(server.URL, server.Client(), nil).SubmitSignedPrivate(context.Background(), signed, request); err != nil {
		t.Fatal(err)
	}
	if received != signedPrivateWire(t, request, signed) {
		t.Fatalf("body = %q", received)
	}
}

func TestSubmitSignedPrivateRejectsRouteMutationBeforeSending(t *testing.T) {
	request, signed := validSignedPrivate(t, PrivateRequestParams{Operation: QueryOrder, Symbol: "BTCUSDT", ClientOrderID: "orbital-order-1"})
	request.VenueMetadata = []byte(`{"method":"DELETE","path":"/fapi/v3/order"}`)
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	defer server.Close()

	_, err := NewClient(server.URL, server.Client(), nil).SubmitSignedPrivate(context.Background(), signed, request)
	if !errors.Is(err, ErrSubmissionNotSent) || called {
		t.Fatalf("error = %v, called = %v", err, called)
	}
}

func TestSubmitSignedPrivateMarksServerFailureAmbiguous(t *testing.T) {
	request, signed := validSignedPrivate(t, PrivateRequestParams{Operation: UpdateLeverage, Symbol: "BTCUSDT", Leverage: 5})
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	_, err := NewClient(server.URL, server.Client(), nil).SubmitSignedPrivate(context.Background(), signed, request)
	if !errors.Is(err, ErrSubmissionAmbiguous) {
		t.Fatalf("error = %v", err)
	}
}

func TestSubmitSignedPrivateMarksAccountSnapshotDepositRequired(t *testing.T) {
	operations := []PrivateOperation{GetPositionMode, GetAccount, GetPositions}
	for _, operation := range operations {
		t.Run(string(operation), func(t *testing.T) {
			request, signed := validSignedPrivate(t, PrivateRequestParams{Operation: operation})
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				response.WriteHeader(http.StatusBadRequest)
				_, _ = response.Write([]byte(`{"code":-5050,"msg":"This function can only be used after deposit"}`))
			}))
			defer server.Close()

			result, err := NewClient(server.URL, server.Client(), nil).SubmitSignedPrivate(context.Background(), signed, request)
			if err != nil || !result.DepositRequired || result.AccountUpdate != nil {
				t.Fatalf("result = %+v, error = %v", result, err)
			}
		})
	}
}

func TestSubmitSignedPrivateRejectsUncorrelatedMutationResponse(t *testing.T) {
	request, signed := validSignedPrivate(t, PrivateRequestParams{Operation: UpdateLeverage, Symbol: "BTCUSDT", Leverage: 5})
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte(`{"leverage":10,"symbol":"ETHUSDT"}`))
	}))
	defer server.Close()

	_, err := NewClient(server.URL, server.Client(), nil).SubmitSignedPrivate(context.Background(), signed, request)
	if !errors.Is(err, ErrSubmissionAmbiguous) {
		t.Fatalf("error = %v", err)
	}
}

func TestValidatePrivateResponseRequiresCorrelatedOrder(t *testing.T) {
	request := &domain.SigningRequest{Symbol: "BTCUSDT", ClientOrderID: "orbital-order-1"}
	valid := []byte(`{"orderId":123,"clientOrderId":"orbital-order-1","symbol":"BTCUSDT","status":"FILLED","executedQty":"1","avgPrice":"100"}`)
	if _, _, err := validatePrivateResponse(QueryOrder, request, valid); err != nil {
		t.Fatal(err)
	}
	invalid := []byte(`{"orderId":123,"clientOrderId":"another-order","symbol":"BTCUSDT","status":"FILLED","executedQty":"1","avgPrice":"100"}`)
	if _, _, err := validatePrivateResponse(QueryOrder, request, invalid); err == nil {
		t.Fatal("uncorrelated order response was accepted")
	}
}

func TestValidatePrivateResponseAcceptsAllowlistedOperationShapes(t *testing.T) {
	tests := []struct {
		operation PrivateOperation
		request   *domain.SigningRequest
		body      string
	}{
		{GetPositionMode, &domain.SigningRequest{}, `{"dualSidePosition":false}`},
		{GetAccount, &domain.SigningRequest{}, `{"canTrade":true,"totalMarginBalance":"100","availableBalance":"90","positions":[]}`},
		{GetPositions, &domain.SigningRequest{Symbol: "BTCUSDT"}, `[{"symbol":"BTCUSDT","positionSide":"BOTH","positionAmt":"0","entryPrice":"0","liquidationPrice":"0","leverage":"5"}]`},
		{GetLeverageBracket, &domain.SigningRequest{Symbol: "BTCUSDT"}, `{"symbol":"BTCUSDT","brackets":[{"initialLeverage":5,"notionalCap":100000,"notionalFloor":0}]}`},
		{GetIncome, &domain.SigningRequest{Account: testUser}, `[{"symbol":"BTCUSDT","incomeType":"FUNDING_FEE","income":"0.01","asset":"USDT","time":1570636800000,"tranId":"9689322392"}]`},
		{QueryOrder, &domain.SigningRequest{Symbol: "BTCUSDT", ClientOrderID: "orbital-order-1"}, `{"orderId":123,"clientOrderId":"orbital-order-1","symbol":"BTCUSDT","status":"NEW","executedQty":"0","avgPrice":"0"}`},
		{UpdateLeverage, &domain.SigningRequest{Symbol: "BTCUSDT", Leverage: 5}, `{"symbol":"BTCUSDT","leverage":5}`},
		{StartUserStream, &domain.SigningRequest{}, `{"listenKey":"0123456789abcdef"}`},
		{KeepaliveUserStream, &domain.SigningRequest{}, `{}`},
		{CloseUserStream, &domain.SigningRequest{}, `{}`},
	}
	for _, test := range tests {
		t.Run(string(test.operation), func(t *testing.T) {
			if _, _, err := validatePrivateResponse(test.operation, test.request, []byte(test.body)); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestValidatePrivateResponseNormalizesAsterFundingPayments(t *testing.T) {
	_, payments, err := validatePrivateResponse(GetIncome, &domain.SigningRequest{Account: testUser}, []byte(
		`[{"symbol":"2ZUSDT","incomeType":"FUNDING_FEE","income":"-0.0125","asset":"USDT","time":1570636800000,"tranId":"9689322392"}]`,
	))
	if err != nil {
		t.Fatal(err)
	}
	if len(payments) != 1 || payments[0].ExternalID != "9689322392" || payments[0].MarketKey != "2ZUSDT" ||
		payments[0].Asset != "2Z" || payments[0].AmountUSD != -0.0125 {
		t.Fatalf("payments = %+v", payments)
	}
	_, _, err = validatePrivateResponse(GetIncome, &domain.SigningRequest{Account: testUser}, []byte(
		`[{"symbol":"2ZUSDT","incomeType":"COMMISSION","income":"-0.0125","asset":"USDT","time":1570636800000,"tranId":"9689322392"}]`,
	))
	if err == nil {
		t.Fatal("non-funding income was accepted")
	}
}

func validSignedPrivate(t *testing.T, params PrivateRequestParams) (*domain.SigningRequest, domain.SignedAction) {
	t.Helper()
	params.User, params.Signer = testUser, testSigner
	now := time.Now()
	request, err := buildPrivatePayload(params, now, now.UnixMicro())
	if err != nil {
		t.Fatal(err)
	}
	return request, domain.SignedAction{
		RequestID: request.ID, ClientOrderID: request.ClientOrderID, Venue: "aster",
		SignerAddress: testSigner, Signature: "0x" + strings.Repeat("1", 128) + "1b",
	}
}

func signedPrivateWire(t *testing.T, request *domain.SigningRequest, signed domain.SignedAction) string {
	t.Helper()
	var unsigned AsterUnsignedOrder
	if err := json.Unmarshal(request.UnsignedPayload, &unsigned); err != nil {
		t.Fatal(err)
	}
	return unsigned.Message.Msg + "&signature=" + signed.Signature
}
