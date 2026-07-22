package api

import (
	"math"
	"testing"
	"time"
)

func TestChartRangesUseBoundedHistorySources(t *testing.T) {
	tests := map[string]historySource{
		"24h": source5m,
		"7d":  source1h,
		"30d": source1h,
	}
	for rangeName, want := range tests {
		if got := rangeSpec[rangeName].source; got != want {
			t.Errorf("range %s source = %v, want %v", rangeName, got, want)
		}
	}
}

func TestPairHistoryRowsIncludesVenueFunding(t *testing.T) {
	ts := time.Now().UTC().Truncate(time.Minute).Unix()
	points := pairHistoryRows(
		[]historyRow{{TsUnix: ts, MarkPrice: 100, FundingRate: 0.0002}},
		[]historyRow{{TsUnix: ts, MarkPrice: 101, FundingRate: -0.0001}},
		sourceRaw,
	)
	if len(points) != 1 {
		t.Fatalf("points = %d, want 1", len(points))
	}
	point := points[0]
	if point.FundingA != 0.0002 || point.FundingB != -0.0001 {
		t.Fatalf("funding = %v/%v, want 0.0002/-0.0001", point.FundingA, point.FundingB)
	}
	wantEdge := 0.0003 * 8760
	if math.Abs(point.Edge-wantEdge) > 1e-9 {
		t.Fatalf("edge = %v, want %v", point.Edge, wantEdge)
	}
}

func TestDownsampleHistoryPointsCapsResponse(t *testing.T) {
	points := make([]historyPoint, 720)
	for i := range points {
		points[i].T = time.Unix(int64(i), 0).UTC().Format(time.RFC3339)
	}
	got := downsampleHistoryPoints(points, 200)
	if len(got) != 200 {
		t.Fatalf("downsampled points = %d, want 200", len(got))
	}
	if got[0].T != points[0].T || got[len(got)-1].T != points[len(points)-1].T {
		t.Fatal("downsampled points must preserve the first and latest samples")
	}
}
