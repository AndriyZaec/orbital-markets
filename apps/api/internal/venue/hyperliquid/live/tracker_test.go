package live

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"
)

func TestFilledSubmitResponseSeedsTrackerFill(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client := NewClient(logger, nil, nil, nil, nil)
	now := time.Now()
	result := client.parseResponse([]byte(`{
		"status":"ok",
		"response":{"type":"order","data":{"statuses":[
			{"filled":{"totalSz":"25.1","avgPx":"0.5974","oid":531535222273}}
		]}}
	}`), "VIRTUAL", "0xclose", now, now)

	tracker := NewTracker(logger, "0xaccount")
	tracker.Register(result, 25.1)
	fill, err := tracker.WaitForFill(context.Background(), "0xclose")
	if err != nil {
		t.Fatal(err)
	}
	if fill.Status != OrderStatusFilled || fill.FilledAmount != 25.1 || fill.AvgFillPrice != 0.5974 {
		t.Fatalf("fill = %+v, want terminal REST fill", fill)
	}
}
