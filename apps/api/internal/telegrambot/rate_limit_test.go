package telegrambot

import (
	"testing"
	"time"
)

func TestActionLimiterAppliesGlobalLimit(t *testing.T) {
	now := time.Date(2026, time.August, 17, 12, 0, 0, 0, time.UTC)
	limiter := newActionLimiter(func() time.Time { return now })

	for chatID := int64(1); chatID <= int64(globalActionBurst); chatID++ {
		if !limiter.allowAction(chatID) {
			t.Fatalf("action %d was unexpectedly limited", chatID)
		}
	}
	if limiter.allowAction(999) {
		t.Fatal("action beyond global burst was allowed")
	}

	now = now.Add(time.Second / time.Duration(globalActionRate))
	if !limiter.allowAction(999) {
		t.Fatal("global token did not replenish")
	}
}

func TestActionLimiterBoundsTrackedChats(t *testing.T) {
	now := time.Date(2026, time.August, 17, 12, 0, 0, 0, time.UTC)
	limiter := newActionLimiter(func() time.Time { return now })
	for chatID := int64(1); chatID <= maxLimitedChats+100; chatID++ {
		limiter.allowAction(chatID)
	}
	if len(limiter.chats) != maxLimitedChats {
		t.Fatalf("tracked chats = %d, want %d", len(limiter.chats), maxLimitedChats)
	}
}
