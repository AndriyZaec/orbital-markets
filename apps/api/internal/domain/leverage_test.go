package domain

import "testing"

func TestComputeLeverageDoesNotClampValidVenueLeverage(t *testing.T) {
	cfg := ComputeLeverage(1000, 10)
	if cfg.Leverage != 10 {
		t.Fatalf("Leverage = %v, want 10", cfg.Leverage)
	}
	if cfg.MarginRequired != 100 {
		t.Fatalf("MarginRequired = %v, want 100", cfg.MarginRequired)
	}
}

func TestValidateLeverageUsesPairMaximum(t *testing.T) {
	if !ValidateLeverage(10, 10) {
		t.Fatal("ValidateLeverage(10, 10) = false, want true")
	}
	if ValidateLeverage(10, 5) {
		t.Fatal("ValidateLeverage(10, 5) = true, want false")
	}
	if ValidateLeverage(0.5, 10) {
		t.Fatal("ValidateLeverage(0.5, 10) = true, want false")
	}
}
