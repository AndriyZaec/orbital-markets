package api

import "testing"

func TestMergeNormFillsCalculatesAggregateHedge(t *testing.T) {
	first := &normFill{FilledAmount: 4, AvgFillPrice: 100, Fee: 0.4, Filled: true}
	retry := &normFill{FilledAmount: 5.7, AvgFillPrice: 102, Fee: 0.6, Filled: true}

	merged := mergeNormFills(first, retry)
	if merged.FilledAmount != 9.7 {
		t.Fatalf("filled amount = %v, want 9.7", merged.FilledAmount)
	}
	wantPrice := (4*100 + 5.7*102) / 9.7
	if diff := merged.AvgFillPrice - wantPrice; diff < -1e-9 || diff > 1e-9 {
		t.Fatalf("average price = %v, want %v", merged.AvgFillPrice, wantPrice)
	}
	if mismatch := hedgeMismatch(10, merged.FilledAmount); mismatch > maxHedgeMismatchPct {
		t.Fatalf("mismatch = %v, want within tolerance", mismatch)
	}
}

func TestRemainingFillAfterPartialUnwindTracksExactExposure(t *testing.T) {
	fill := &normFill{FilledAmount: 10, AvgFillPrice: 100, Filled: true}
	remaining := remainingFillAfterUnwind(fill, 7.25)
	if remaining == nil || remaining.FilledAmount != 2.75 {
		t.Fatalf("remaining fill = %+v, want 2.75", remaining)
	}
	if fullyClosed := remainingFillAfterUnwind(fill, 10); fullyClosed != nil {
		t.Fatalf("fully closed fill = %+v, want nil", fullyClosed)
	}
}

func TestRetryableLeg2FillAllowsKnownZeroFillButNotTimeout(t *testing.T) {
	if !retryableLeg2Fill(&normFill{Status: "rejected"}, 10) {
		t.Fatal("known zero-fill rejection should receive one retry")
	}
	if retryableLeg2Fill(&normFill{Status: "timeout"}, 10) {
		t.Fatal("ambiguous timeout should reconcile instead of retrying blindly")
	}
}

func TestUnwindConfirmationDoesNotHideResidualExposure(t *testing.T) {
	if unwindFullyFilled(10, 9.96) {
		t.Fatal("99.6% unwind must preserve the remaining exposure")
	}
	if !unwindFullyFilled(10, 10) {
		t.Fatal("full unwind was not confirmed")
	}
}
