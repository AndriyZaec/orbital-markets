package account

import (
	"testing"
)

func TestParseRESTAccountInfo(t *testing.T) {
	raw := []byte(`{
		"success": true,
		"data": {
			"balance": "46.159179",
			"account_equity": "46.159179",
			"available_to_spend": "46.159179",
			"available_to_withdraw": "46.159179",
			"total_margin_used": "0",
			"cross_mmr": "0",
			"updated_at": 1784712476141
		},
		"error": null,
		"code": null
	}`)

	info, err := parseRESTAccountInfo(raw)
	if err != nil {
		t.Fatalf("parseRESTAccountInfo() error = %v", err)
	}
	if info.Equity != 46.159179 {
		t.Fatalf("Equity = %v, want 46.159179", info.Equity)
	}
	if info.AvailableToSpend != 46.159179 {
		t.Fatalf("AvailableToSpend = %v, want 46.159179", info.AvailableToSpend)
	}
	if info.AvailableToWithdraw != 46.159179 {
		t.Fatalf("AvailableToWithdraw = %v, want 46.159179", info.AvailableToWithdraw)
	}
}
