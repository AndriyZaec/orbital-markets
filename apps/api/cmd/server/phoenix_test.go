package main

import (
	"strings"
	"testing"
)

func TestPhoenixConfigurationIsOptionalAndAllOrNothing(t *testing.T) {
	for _, tc := range []struct {
		name    string
		values  map[string]string
		enabled bool
		invalid bool
	}{
		{"absent", nil, false, false},
		{"partial key", map[string]string{"PHOENIX_RELAYER_PRIVATE_KEY": "test-secret"}, false, true},
		{"partial URL", map[string]string{"HELIUS_RPC_URL": "https://rpc.example/?api-key=test-secret"}, false, true},
		{"partial builder", map[string]string{"PHOENIX_BUILDER_AUTHORITY": "11111111111111111111111111111111"}, false, true},
		{"complete", map[string]string{"PHOENIX_RELAYER_PRIVATE_KEY": "test-secret", "HELIUS_RPC_URL": "https://rpc.example/?api-key=test-secret", "PHOENIX_MAX_TRANSACTION_FEE_LAMPORTS": "10000"}, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config, enabled, err := phoenixConfig(func(key string) string { return tc.values[key] })
			if enabled != tc.enabled || (err != nil) != tc.invalid {
				t.Fatalf("enabled=%t err=%v", enabled, err)
			}
			if err != nil && strings.Contains(err.Error(), "test-secret") {
				t.Fatal("configuration error leaked secret")
			}
			if enabled && (config.HelperPath != "/phoenix-helper" || config.MaxTransactionFeeLamports != 10000) {
				t.Fatal("unexpected defaults/cap")
			}
		})
	}
}

func TestPhoenixCapRequiresExplicitPositiveInteger(t *testing.T) {
	for _, cap := range []string{"0", "-1", "1.5", "invalid", "18446744073709551616"} {
		_, enabled, err := phoenixConfig(func(key string) string {
			switch key {
			case "PHOENIX_RELAYER_PRIVATE_KEY":
				return "test-secret"
			case "HELIUS_RPC_URL":
				return "https://rpc.example"
			case "PHOENIX_MAX_TRANSACTION_FEE_LAMPORTS":
				return cap
			}
			return ""
		})
		if enabled || err == nil {
			t.Fatalf("invalid cap accepted: %s", cap)
		}
	}
}

func TestPhoenixComputeBudgetConfiguration(t *testing.T) {
	for _, tc := range []struct {
		units, price, cap string
		valid             bool
	}{
		{"", "", "10000", true}, {"500000", "1234", "20000", true},
		{"1400000", "1000000", "2000000", true}, {"0", "0", "10000", false},
		{"1400001", "0", "10000", false}, {"400000", "1000001", "2000000", false},
		{"400000", "-1", "10000", false}, {"400000", "1.5", "10000", false},
		{"4294967296", "0", "10000", false}, {"400000", "1000000", "400000", false},
	} {
		values := map[string]string{"PHOENIX_RELAYER_PRIVATE_KEY": "test-secret", "HELIUS_RPC_URL": "https://rpc.example", "PHOENIX_MAX_TRANSACTION_FEE_LAMPORTS": tc.cap,
			"PHOENIX_COMPUTE_UNIT_LIMIT": tc.units, "PHOENIX_COMPUTE_UNIT_PRICE_MICRO_LAMPORTS": tc.price}
		config, enabled, err := phoenixConfig(func(k string) string { return values[k] })
		if enabled != tc.valid || (err == nil) != tc.valid {
			t.Fatalf("%+v: enabled=%v err=%v", tc, enabled, err)
		}
		if tc.valid && (config.OrderComputeBudget.UnitLimit == 0 || config.OrderComputeBudget.UnitPriceMicroLamports > 1_000_000) {
			t.Fatal("invalid resolved budget")
		}
	}
}
