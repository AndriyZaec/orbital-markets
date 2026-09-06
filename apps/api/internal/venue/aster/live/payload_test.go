package live

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/aster"
)

const (
	testUser    = "0x1111111111111111111111111111111111111111"
	testSigner  = "0x2222222222222222222222222222222222222222"
	testBuilder = "0x3333333333333333333333333333333333333333"
)

type testRuleMap struct {
	rules aster.OrderRules
}

func (m testRuleMap) OrderRules(string) (aster.OrderRules, bool) {
	return m.rules, true
}

var payloadTestRules = testRuleMap{rules: aster.OrderRules{
	MinPrice: "0.1", MaxPrice: "1000000", TickSize: "0.1",
	MinQuantity: "0.001", MaxQuantity: "1000", QuantityStep: "0.001",
	MinNotional: "5",
}}

func TestBuildOpenPayloadMatchesAsterCodeSigningFixture(t *testing.T) {
	now := time.Unix(0, 1700000000123456789)
	request, err := buildPayload(
		payloadTestRules, testUser, testSigner, "BTCUSDT", domain.SideLong,
		2.1239, 100.001, "orbital-l1open-1700000000000000000",
		false, "open", openSlippageBPS,
		&BuilderConfig{Address: testBuilder, FeeRate: "0.0002"},
		now, 1700000000123456,
	)
	if err != nil {
		t.Fatal(err)
	}
	if request.Amount != 2.123 || request.Side != "buy" || request.ReduceOnly {
		t.Fatalf("request summary = %+v", request)
	}
	var unsigned AsterUnsignedOrder
	if err := json.Unmarshal(request.UnsignedPayload, &unsigned); err != nil {
		t.Fatal(err)
	}
	want := "symbol=BTCUSDT&type=LIMIT&builder=" + testBuilder +
		"&feeRate=0.0002&side=BUY&quantity=2.123&price=100.6&timeInForce=IOC" +
		"&newClientOrderId=orbital-l1open-1700000000000000000&reduceOnly=false" +
		"&positionSide=BOTH&asterChain=Mainnet&user=" + testUser +
		"&signer=" + testSigner + "&nonce=1700000000123456"
	if unsigned.Message.Msg != want {
		t.Fatalf("signed query = %q, want %q", unsigned.Message.Msg, want)
	}
	if unsigned.PrimaryType != "Message" || unsigned.Domain.ChainID != 1666 ||
		unsigned.Domain.Name != "AsterSignTransaction" {
		t.Fatalf("typed data = %+v", unsigned)
	}
}

func TestBuildUnwindPayloadIsReduceOnlyAndOmitsBuilderFee(t *testing.T) {
	request, err := BuildUnwindPayload(
		payloadTestRules, testUser, testSigner, "BTCUSDT", domain.SideLong,
		2.1239, 100.001, "orbital-l1unwind-1700000000000000000",
	)
	if err != nil {
		t.Fatal(err)
	}
	var unsigned AsterUnsignedOrder
	if err := json.Unmarshal(request.UnsignedPayload, &unsigned); err != nil {
		t.Fatal(err)
	}
	if request.Side != "sell" || !request.ReduceOnly || request.Action != "unwind" {
		t.Fatalf("request summary = %+v", request)
	}
	if strings.Contains(unsigned.Message.Msg, "builder=") || strings.Contains(unsigned.Message.Msg, "feeRate=") {
		t.Fatalf("recovery query charges builder fee: %s", unsigned.Message.Msg)
	}
	if !strings.Contains(unsigned.Message.Msg, "side=SELL&quantity=2.123&price=99&timeInForce=IOC") ||
		!strings.Contains(unsigned.Message.Msg, "reduceOnly=true&positionSide=BOTH") {
		t.Fatalf("unwind query = %s", unsigned.Message.Msg)
	}
}

func TestBuildUnwindPayloadAllowsDustRecovery(t *testing.T) {
	request, err := BuildUnwindPayload(
		payloadTestRules, testUser, testSigner, "BTCUSDT", domain.SideShort,
		0.001, 100, "recovery/order:1",
	)
	if err != nil {
		t.Fatal(err)
	}
	var unsigned AsterUnsignedOrder
	if err := json.Unmarshal(request.UnsignedPayload, &unsigned); err != nil {
		t.Fatal(err)
	}
	if request.Side != "buy" || !strings.Contains(unsigned.Message.Msg, "newClientOrderId=recovery%2Forder%3A1") {
		t.Fatalf("dust recovery request = %+v, query = %s", request, unsigned.Message.Msg)
	}
}

func TestBuildOpenPayloadRejectsOrdersBelowMinimumNotional(t *testing.T) {
	_, err := BuildOpenPayload(
		payloadTestRules, testUser, testSigner, "BTCUSDT", domain.SideLong,
		0.0019, 100, "client-order",
		BuilderConfig{Address: testBuilder, FeeRate: "0.0002"},
	)
	if err == nil || !strings.Contains(err.Error(), "below minimum") {
		t.Fatalf("error = %v, want minimum-notional rejection", err)
	}
}

func TestBuildOpenPayloadRejectsUnsafeBuilderFee(t *testing.T) {
	_, err := BuildOpenPayload(
		payloadTestRules, testUser, testSigner, "BTCUSDT", domain.SideLong,
		1, 100, "client-order",
		BuilderConfig{Address: testBuilder, FeeRate: "0.0011"},
	)
	if err == nil || !strings.Contains(err.Error(), "builder fee rate") {
		t.Fatalf("error = %v, want builder-fee rejection", err)
	}
}

func TestAsterNoncesRemainUniqueWithinOneMicrosecond(t *testing.T) {
	previous := nextAsterNonce()
	for range 100 {
		next := nextAsterNonce()
		if next <= previous {
			t.Fatalf("nonce %d did not advance after %d", next, previous)
		}
		previous = next
	}
}

func TestNormalizeQuantityToleratesFloatBoundaryNoise(t *testing.T) {
	quantity, wire, err := normalizeQuantity(0.3-0.2, aster.OrderRules{
		MinQuantity: "0.1", MaxQuantity: "100", QuantityStep: "0.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if quantity != 0.1 || wire != "0.1" {
		t.Fatalf("quantity = %v (%q), want 0.1", quantity, wire)
	}
}
