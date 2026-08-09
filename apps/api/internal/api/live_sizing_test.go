package api

import "testing"

func TestLiveBaseAmountConvertsDollarNotional(t *testing.T) {
	amount, err := liveBaseAmount(25, 1.25)
	if err != nil {
		t.Fatal(err)
	}
	if amount != 20 {
		t.Fatalf("base amount = %v, want 20", amount)
	}
}

func TestLiveBaseAmountRejectsInvalidInputs(t *testing.T) {
	for _, input := range []struct {
		notional float64
		price    float64
	}{
		{notional: 0, price: 1},
		{notional: 25, price: 0},
	} {
		if _, err := liveBaseAmount(input.notional, input.price); err == nil {
			t.Fatalf("liveBaseAmount(%v, %v) accepted invalid input", input.notional, input.price)
		}
	}
}
