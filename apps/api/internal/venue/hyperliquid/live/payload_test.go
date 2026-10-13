package live

import (
	"encoding/json"
	"strconv"
	"testing"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
)

type payloadTestAssetMap struct {
	index    int
	decimals int
}

func TestExplicitOpenLimitsRoundInsideTheBoundAndMatchTheSignedWirePrice(t *testing.T) {
	for _, test := range []struct {
		side  domain.Side
		limit float64
		wire  string
	}{
		{domain.SideLong, 100.5555, "100.55"},
		{domain.SideShort, 99.4444, "99.445"},
		{domain.SideLong, 1.001, "1.001"},
		{domain.SideShort, 1.001, "1.001"},
		{domain.SideLong, 60456.123, "60456"},
		{domain.SideLong, 100000.5, "100000"},
		{domain.SideShort, 100000.5, "100001"},
	} {
		request, err := BuildOpenPayload(payloadTestAssetMap{index: 7, decimals: 3}, "BTC", test.side, 1, test.limit, "bounded", test.limit)
		if err != nil {
			t.Fatal(err)
		}
		var payload HyperliquidUnsignedAction
		if err := json.Unmarshal(request.UnsignedPayload, &payload); err != nil {
			t.Fatal(err)
		}
		wire := payload.Action.Orders[0].LimitPx
		value, err := strconv.ParseFloat(wire, 64)
		if err != nil || wire != test.wire || request.LimitPrice != value {
			t.Fatalf("limit=%g wire=%q summary=%g", test.limit, wire, request.LimitPrice)
		}
		if (test.side == domain.SideLong && value > test.limit) || (test.side == domain.SideShort && value < test.limit) {
			t.Fatal("wire price widened the explicit bound")
		}
	}
}

func TestReduceOnlyIntegerCollarsPreserveZeros(t *testing.T) {
	for _, test := range []struct {
		positionSide domain.Side
		wire         string
	}{
		{domain.SideLong, "59700"},
		{domain.SideShort, "60300"},
	} {
		request, err := BuildUnwindPayload(payloadTestAssetMap{index: 7, decimals: 3}, "BTC", test.positionSide, 1, 60000, "unwind")
		if err != nil {
			t.Fatal(err)
		}
		var payload HyperliquidUnsignedAction
		if err := json.Unmarshal(request.UnsignedPayload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Action.Orders[0].LimitPx != test.wire {
			t.Fatalf("wire=%s want=%s", payload.Action.Orders[0].LimitPx, test.wire)
		}
	}
}

func (m payloadTestAssetMap) AssetIndex(string) (int, bool)   { return m.index, true }
func (m payloadTestAssetMap) SizeDecimals(string) (int, bool) { return m.decimals, true }

func TestBuildL1TypedDataMatchesOfficialSDKFixture(t *testing.T) {
	action := OrderAction{
		Type: "order",
		Orders: []OrderSpec{{
			Asset: 1, IsBuy: false, LimitPx: "101.500000", Size: "2.000000",
			ReduceOnly: false, OrderType: OrderType{Limit: LimitSpec{Tif: "Ioc"}},
			Cloid: "0x00000000000000000000000000000001",
		}},
		Grouping: "na",
	}
	typed, err := buildL1TypedData(action, 1700000000000)
	if err != nil {
		t.Fatal(err)
	}
	if typed.PrimaryType != "Agent" {
		t.Fatalf("primary type = %q, want Agent", typed.PrimaryType)
	}
	if typed.Message.ConnectionID != "0x030c6c348229c1ba1f210242ad159bbb1e918c02f5addda9b179bff7086b7ba5" {
		t.Fatalf("connection id = %q", typed.Message.ConnectionID)
	}
}

func TestHyperliquidNoncesRemainUniqueWithinOneMillisecond(t *testing.T) {
	previous := nextHyperliquidNonce()
	for range 100 {
		next := nextHyperliquidNonce()
		if next <= previous {
			t.Fatalf("nonce %d did not advance after %d", next, previous)
		}
		previous = next
	}
}

func TestBuildOpenPayloadUsesAssetSizePrecision(t *testing.T) {
	request, err := BuildOpenPayload(
		payloadTestAssetMap{index: 7, decimals: 3},
		"VIRTUAL", domain.SideLong, 20.1239, 1.25, "client-order",
	)
	if err != nil {
		t.Fatal(err)
	}
	if request.Amount != 20.123 {
		t.Fatalf("request amount = %v, want 20.123", request.Amount)
	}
	var unsigned HyperliquidUnsignedAction
	if err := json.Unmarshal(request.UnsignedPayload, &unsigned); err != nil {
		t.Fatal(err)
	}
	if unsigned.Action.Orders[0].Size != "20.123" {
		t.Fatalf("wire size = %q, want 20.123", unsigned.Action.Orders[0].Size)
	}
}

func TestBuildOpenPayloadIncludesBuilderCodeInSignedAction(t *testing.T) {
	request, err := BuildOpenPayload(
		payloadTestAssetMap{index: 7, decimals: 3},
		"VIRTUAL", domain.SideLong, 20.123, 1.25, "client-order",
	)
	if err != nil {
		t.Fatal(err)
	}
	var unsigned HyperliquidUnsignedAction
	if err := json.Unmarshal(request.UnsignedPayload, &unsigned); err != nil {
		t.Fatal(err)
	}
	if unsigned.Action.Builder == nil || unsigned.Action.Builder.Address != OrbitalBuilder.Address || unsigned.Action.Builder.Fee != OrbitalBuilder.Fee {
		t.Fatalf("builder = %+v", unsigned.Action.Builder)
	}
}

func TestBuildRecoveryPayloadsOmitBuilderCode(t *testing.T) {
	builders := []struct {
		name   string
		action string
		build  func(AssetMap, string, domain.Side, float64, float64, string) (*domain.SigningRequest, error)
	}{
		{name: "unwind", action: "unwind", build: BuildUnwindPayload},
		{name: "emergency close", action: "emergency_close", build: BuildEmergencyClosePayload},
	}
	for _, test := range builders {
		t.Run(test.name, func(t *testing.T) {
			request, err := test.build(payloadTestAssetMap{index: 7, decimals: 3}, "VIRTUAL", domain.SideLong, 20.123, 1.25, "client-order")
			if err != nil {
				t.Fatal(err)
			}
			var unsigned HyperliquidUnsignedAction
			if err := json.Unmarshal(request.UnsignedPayload, &unsigned); err != nil {
				t.Fatal(err)
			}
			if request.Action != test.action || !request.ReduceOnly || unsigned.Action.Builder != nil {
				t.Fatalf("request = %+v, action = %+v", request, unsigned.Action)
			}
		})
	}
}

func TestBuildOpenPayloadUsesHyperliquidPricePrecision(t *testing.T) {
	request, err := BuildOpenPayload(
		payloadTestAssetMap{index: 7, decimals: 1},
		"VIRTUAL", domain.SideShort, 45.6, 0.547, "client-order",
	)
	if err != nil {
		t.Fatal(err)
	}
	var unsigned HyperliquidUnsignedAction
	if err := json.Unmarshal(request.UnsignedPayload, &unsigned); err != nil {
		t.Fatal(err)
	}
	if unsigned.Action.Orders[0].LimitPx != "0.54427" {
		t.Fatalf("wire price = %q, want 0.54427", unsigned.Action.Orders[0].LimitPx)
	}
}

func TestBuildUpdateLeveragePayloadUsesCrossMargin(t *testing.T) {
	request, err := BuildUpdateLeveragePayload(payloadTestAssetMap{index: 7, decimals: 1}, "0xowner", "VIRTUAL", 2)
	if err != nil {
		t.Fatal(err)
	}
	var payload HyperliquidUnsignedLeverage
	if err := json.Unmarshal(request.UnsignedPayload, &payload); err != nil {
		t.Fatal(err)
	}
	if request.Action != "update_leverage" || request.Leverage != 2 || payload.Action.Asset != 7 || !payload.Action.IsCross || payload.Action.Leverage != 2 {
		t.Fatalf("request = %+v payload = %+v", request, payload)
	}
}

func TestAttachSignatureBuildsHyperliquidSignatureObject(t *testing.T) {
	signature := "0x" +
		"1111111111111111111111111111111111111111111111111111111111111111" +
		"2222222222222222222222222222222222222222222222222222222222222222" +
		"1b"
	body, err := AttachSignature(domain.SignedAction{Signature: signature}, HyperliquidUnsignedAction{
		Action: OrderAction{Type: "order", Grouping: "na"},
		Nonce:  1700000000000,
	})
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Signature struct {
			R string `json:"r"`
			S string `json:"s"`
			V int    `json:"v"`
		} `json:"signature"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Signature.R != "0x1111111111111111111111111111111111111111111111111111111111111111" ||
		decoded.Signature.S != "0x2222222222222222222222222222222222222222222222222222222222222222" ||
		decoded.Signature.V != 27 {
		t.Fatalf("signature = %+v", decoded.Signature)
	}
}
