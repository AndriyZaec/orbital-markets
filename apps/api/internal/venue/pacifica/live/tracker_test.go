package live

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"
)

func TestWaitForFillUsesTerminalOrderUpdateBeforeTradeEvent(t *testing.T) {
	tracker := NewTracker(slog.New(slog.NewTextHandler(io.Discard, nil)))
	tracker.Register(&SubmitResult{
		OrderID: "42", ClientOrderID: "client-1", Symbol: "SOL",
		Accepted: true, SubmittedAt: time.Now(),
	}, 1)
	tracker.HandleOrderUpdate([]byte(`[{"i":42,"I":"client-1","s":"SOL","a":"1","f":"1","os":"filled","p":"100"}]`))

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	fill, err := tracker.WaitForFill(ctx, "client-1")
	if err != nil {
		t.Fatal(err)
	}
	if fill.Status != OrderStatusFilled || fill.FilledAmount != 1 || fill.AvgFillPrice != 100 {
		t.Fatalf("fill = %+v, want terminal 1 SOL at $100", fill)
	}
}

func TestWaitForFillDeduplicatesTradeEventsByHistoryID(t *testing.T) {
	tracker := NewTracker(slog.New(slog.NewTextHandler(io.Discard, nil)))
	tracker.Register(&SubmitResult{
		OrderID: "42", ClientOrderID: "client-1", Symbol: "SOL",
		Accepted: true, SubmittedAt: time.Now(),
	}, 1)
	trade := []byte(`[{"h":77,"i":42,"I":"client-1","s":"SOL","p":"100","a":"1","f":"0.25","t":1765018588190}]`)
	tracker.HandleTrade(trade)
	tracker.HandleTrade(trade)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	fill, err := tracker.WaitForFill(ctx, "client-1")
	if err != nil {
		t.Fatal(err)
	}
	if fill.FilledAmount != 1 || fill.TotalFee != 0.25 || fill.FillCount != 1 {
		t.Fatalf("fill = %+v, want one deduplicated trade", fill)
	}
}
