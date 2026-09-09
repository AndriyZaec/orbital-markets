package live

import (
	"encoding/json"
	"testing"
	"time"
)

func TestBuildPrivatePayloadMatchesAllowlistedAsterOperations(t *testing.T) {
	now := time.Unix(1_700_000_000, 123_000_000)
	tests := []struct {
		name      string
		params    PrivateRequestParams
		method    string
		path      string
		wantQuery string
	}{
		{"position mode", PrivateRequestParams{Operation: GetPositionMode}, "GET", "/fapi/v3/positionSide/dual", ""},
		{"account", PrivateRequestParams{Operation: GetAccount}, "GET", "/fapi/v3/accountWithJoinMargin", ""},
		{"positions", PrivateRequestParams{Operation: GetPositions, Symbol: "BTCUSDT"}, "GET", "/fapi/v3/positionRisk", "symbol=BTCUSDT&"},
		{"brackets", PrivateRequestParams{Operation: GetLeverageBracket}, "GET", "/fapi/v3/leverageBracket", ""},
		{"query order", PrivateRequestParams{Operation: QueryOrder, Symbol: "BTCUSDT", ClientOrderID: "orbital-order-1"}, "GET", "/fapi/v3/order", "symbol=BTCUSDT&origClientOrderId=orbital-order-1&"},
		{"leverage", PrivateRequestParams{Operation: UpdateLeverage, Symbol: "BTCUSDT", Leverage: 5}, "POST", "/fapi/v3/leverage", "symbol=BTCUSDT&leverage=5&"},
		{"start stream", PrivateRequestParams{Operation: StartUserStream}, "POST", "/fapi/v3/listenKey", ""},
		{"keepalive stream", PrivateRequestParams{Operation: KeepaliveUserStream}, "PUT", "/fapi/v3/listenKey", ""},
		{"close stream", PrivateRequestParams{Operation: CloseUserStream}, "DELETE", "/fapi/v3/listenKey", ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			params := test.params
			params.User, params.Signer = testUser, testSigner
			request, err := buildPrivatePayload(params, now, 1_700_000_000_123_456)
			if err != nil {
				t.Fatal(err)
			}
			var unsigned AsterUnsignedOrder
			if err := json.Unmarshal(request.UnsignedPayload, &unsigned); err != nil {
				t.Fatal(err)
			}
			wantQuery := test.wantQuery + "asterChain=Mainnet&user=" + testUser + "&signer=" + testSigner + "&nonce=1700000000123456"
			if unsigned.Message.Msg != wantQuery {
				t.Fatalf("query = %q, want %q", unsigned.Message.Msg, wantQuery)
			}
			var metadata AsterPrivateSubmitMeta
			if err := json.Unmarshal(request.VenueMetadata, &metadata); err != nil {
				t.Fatal(err)
			}
			if metadata.Method != test.method || metadata.Path != test.path || request.Action != string(params.Operation) {
				t.Fatalf("request = %+v, metadata = %+v", request, metadata)
			}
		})
	}
}

func TestBuildPrivatePayloadRejectsParametersOutsideOperationPolicy(t *testing.T) {
	_, err := BuildPrivatePayload(PrivateRequestParams{
		Operation: GetAccount, User: testUser, Signer: testSigner, Symbol: "BTCUSDT",
	})
	if err == nil {
		t.Fatal("unexpected account parameter was accepted")
	}
	_, err = BuildPrivatePayload(PrivateRequestParams{
		Operation: UpdateLeverage, User: testUser, Signer: testSigner, Symbol: "BTCUSDT", Leverage: 126,
	})
	if err == nil {
		t.Fatal("unsafe leverage was accepted")
	}
}

func TestBuildAccountSnapshotPayloadsSharesOneGeneration(t *testing.T) {
	payloads, err := BuildAccountSnapshotPayloads(testUser, testSigner)
	if err != nil {
		t.Fatal(err)
	}
	if payloads.SnapshotID == "" || len(payloads.Requests) != 3 {
		t.Fatalf("payloads = %+v", payloads)
	}
	wantActions := []string{"get_position_mode", "get_account", "get_positions"}
	createdAt := payloads.Requests[0].CreatedAt
	for i, request := range payloads.Requests {
		if request.SnapshotID != payloads.SnapshotID || request.Action != wantActions[i] || !request.CreatedAt.Equal(createdAt) {
			t.Fatalf("request %d = %+v", i, request)
		}
	}
}

func TestBuildAccountRefreshPayloadsOmitsPositionMode(t *testing.T) {
	payloads, err := BuildAccountRefreshPayloads(testUser, testSigner)
	if err != nil {
		t.Fatal(err)
	}
	if !IsAccountRefreshSnapshot(payloads.SnapshotID) || len(payloads.Requests) != 2 {
		t.Fatalf("payloads = %+v", payloads)
	}
	wantActions := []string{"get_account", "get_positions"}
	for i, request := range payloads.Requests {
		if request.SnapshotID != payloads.SnapshotID || request.Action != wantActions[i] {
			t.Fatalf("request %d = %+v", i, request)
		}
	}
}
