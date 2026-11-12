package helper

import (
	"context"
	"testing"
	"time"
)

func TestDecodeAuthorityBindsOwnerAndCrossTrader(t *testing.T) {
	for _, mode := range []string{"valid", "authority_wrong_owner"} {
		c, err := startClient(context.Background(), fakeHelperCommand(t, mode), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		result, err := c.DecodeTraderAuthority(context.Background(), DecodeAuthorityRequest{OwnerAuthority: "owner", TraderAccount: "trader", AccountOwner: PhoenixProgramID, DataBase64: "YQ=="})
		if mode == "valid" && (err != nil || result.OwnerAuthority != "owner" || result.TraderPDAIndex != 0 || result.SubaccountIndex != 0) {
			t.Fatalf("result=%+v %v", result, err)
		}
		if mode != "valid" && err == nil {
			t.Fatal("wrong owner accepted")
		}
		c.Close()
	}
}
