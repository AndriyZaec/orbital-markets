package live

import (
	"encoding/json"
	"testing"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
)

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
