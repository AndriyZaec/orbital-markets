package account

import "testing"

func TestParseAccountStateUsesUnifiedSpotUSDCBalance(t *testing.T) {
	perpRaw := []byte(`{
		"marginSummary": {
			"accountValue": "0.0",
			"totalMarginUsed": "0.0",
			"totalRawUsd": "0.0"
		},
		"crossMarginSummary": {
			"accountValue": "0.0",
			"totalMarginUsed": "0.0",
			"totalRawUsd": "0.0"
		},
		"withdrawable": "0.0",
		"assetPositions": []
	}`)
	spotRaw := []byte(`{
		"balances": [
			{"coin":"USDC","token":0,"total":"99.6","hold":"0.0","entryNtl":"0.0"}
		],
		"tokenToAvailableAfterMaintenance": [[0,"99.6"]]
	}`)

	margin, positions, err := parseAccountState(perpRaw, spotRaw)
	if err != nil {
		t.Fatalf("parseAccountState() error = %v", err)
	}
	if margin.AccountEquity != 99.6 {
		t.Fatalf("AccountEquity = %v, want 99.6", margin.AccountEquity)
	}
	if margin.AvailableBalance != 99.6 {
		t.Fatalf("AvailableBalance = %v, want 99.6", margin.AvailableBalance)
	}
	if len(positions) != 0 {
		t.Fatalf("positions = %d, want 0", len(positions))
	}
}

func TestParseAccountStateFallsBackToPerpSummary(t *testing.T) {
	perpRaw := []byte(`{
		"marginSummary": {
			"accountValue": "120.0",
			"totalMarginUsed": "20.0",
			"totalRawUsd": "120.0"
		},
		"withdrawable": "95.0",
		"assetPositions": []
	}`)
	spotRaw := []byte(`{"balances": []}`)

	margin, _, err := parseAccountState(perpRaw, spotRaw)
	if err != nil {
		t.Fatalf("parseAccountState() error = %v", err)
	}
	if margin.AccountEquity != 120 {
		t.Fatalf("AccountEquity = %v, want 120", margin.AccountEquity)
	}
	if margin.AvailableBalance != 100 {
		t.Fatalf("AvailableBalance = %v, want 100", margin.AvailableBalance)
	}
	if margin.Withdrawable != 95 {
		t.Fatalf("Withdrawable = %v, want 95", margin.Withdrawable)
	}
}
