package helper

import "errors"

// These bounds are shared with the Rust builder and browser semantic validator.
const MaxComputeUnitLimit uint32 = 1_400_000
const MaxComputeUnitPriceMicroLamports uint64 = 1_000_000

type ComputeBudget struct {
	UnitLimit              uint32 `json:"unit_limit"`
	UnitPriceMicroLamports uint64 `json:"unit_price_micro_lamports"`
}

func (b ComputeBudget) Effective() ComputeBudget {
	if b == (ComputeBudget{}) {
		b.UnitLimit = 400_000
	}
	return b
}

func (b ComputeBudget) Validate() error {
	if b.UnitLimit == 0 || b.UnitLimit > MaxComputeUnitLimit || b.UnitPriceMicroLamports > MaxComputeUnitPriceMicroLamports {
		return errors.New("Phoenix compute budget exceeds approved bounds")
	}
	return nil
}

func (b ComputeBudget) PriorityFeeLamports() uint64 {
	return (uint64(b.UnitLimit)*b.UnitPriceMicroLamports + 999_999) / 1_000_000
}
