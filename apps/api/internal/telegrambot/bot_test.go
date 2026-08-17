package telegrambot

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
)

type fakeOpportunitySource struct {
	opportunities []domain.Opportunity
}

func (s *fakeOpportunitySource) Opportunities() []domain.Opportunity {
	return append([]domain.Opportunity(nil), s.opportunities...)
}

type sentMessage struct {
	chatID    int64
	messageID int64
	text      string
	keyboard  InlineKeyboardMarkup
}

type fakeMessenger struct {
	sent         []sentMessage
	edited       []sentMessage
	acknowledged []string
}

func (m *fakeMessenger) SendMessage(_ context.Context, chatID int64, text string, keyboard InlineKeyboardMarkup) error {
	m.sent = append(m.sent, sentMessage{chatID: chatID, text: text, keyboard: keyboard})
	return nil
}

func (m *fakeMessenger) EditMessage(_ context.Context, chatID, messageID int64, text string, keyboard InlineKeyboardMarkup) error {
	m.edited = append(m.edited, sentMessage{chatID: chatID, messageID: messageID, text: text, keyboard: keyboard})
	return nil
}

func (m *fakeMessenger) AnswerCallback(_ context.Context, callbackID string) error {
	m.acknowledged = append(m.acknowledged, callbackID)
	return nil
}

func TestOpportunitiesPaginationUsesStableSnapshot(t *testing.T) {
	now := time.Date(2026, time.August, 17, 12, 0, 0, 0, time.UTC)
	source := &fakeOpportunitySource{opportunities: testOpportunities(now, 7)}
	messenger := &fakeMessenger{}
	bot := New(slog.New(slog.NewTextHandler(io.Discard, nil)), source, messenger, "secret", "https://app.example")
	bot.now = func() time.Time { return now }
	bot.snapshots.now = bot.now

	serveUpdate(t, bot, "secret", update{Message: &message{
		MessageID: 1,
		Text:      "/opportunities",
		Chat:      chat{ID: 42, Type: "private"},
	}})
	if len(messenger.sent) != 1 {
		t.Fatalf("sent messages = %d, want 1", len(messenger.sent))
	}
	first := messenger.sent[0]
	if !strings.Contains(first.text, "1. ASSET-1") || strings.Contains(first.text, "6. ASSET-6") {
		t.Fatalf("first page text = %q", first.text)
	}
	if len(first.keyboard.InlineKeyboard) != 2 {
		t.Fatalf("keyboard rows = %d, want 2", len(first.keyboard.InlineKeyboard))
	}
	nextCallback := first.keyboard.InlineKeyboard[0][1].CallbackData
	if !strings.HasPrefix(nextCallback, "opportunities:") {
		t.Fatalf("next callback = %q", nextCallback)
	}

	// A scanner refresh must not reorder an existing Telegram page snapshot.
	source.opportunities = nil
	serveUpdate(t, bot, "secret", update{CallbackQuery: &callbackQuery{
		ID:   "callback-1",
		Data: nextCallback,
		Message: &message{
			MessageID: 9,
			Chat:      chat{ID: 42, Type: "private"},
		},
	}})
	if len(messenger.edited) != 1 || !strings.Contains(messenger.edited[0].text, "6. ASSET-6") {
		t.Fatalf("second page edits = %+v", messenger.edited)
	}
	if len(messenger.acknowledged) != 1 || messenger.acknowledged[0] != "callback-1" {
		t.Fatalf("acknowledged callbacks = %v", messenger.acknowledged)
	}
}

func TestOpportunitiesRefreshCreatesNewSnapshot(t *testing.T) {
	source := &fakeOpportunitySource{opportunities: testOpportunities(time.Now(), 1)}
	messenger := &fakeMessenger{}
	bot := New(slog.New(slog.NewTextHandler(io.Discard, nil)), source, messenger, "secret", "https://app.example")

	source.opportunities = nil
	serveUpdate(t, bot, "secret", update{CallbackQuery: &callbackQuery{
		ID:   "callback-refresh",
		Data: "opportunities:refresh",
		Message: &message{
			MessageID: 11,
			Chat:      chat{ID: 42, Type: "private"},
		},
	}})
	if len(messenger.edited) != 1 || !strings.Contains(messenger.edited[0].text, "No opportunities available") {
		t.Fatalf("refresh edits = %+v", messenger.edited)
	}
}

func TestWebhookRejectsWrongSecret(t *testing.T) {
	bot := New(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		&fakeOpportunitySource{},
		&fakeMessenger{},
		"expected",
		"https://app.example",
	)
	body, _ := json.Marshal(update{Message: &message{Text: "/start", Chat: chat{ID: 1, Type: "private"}}})
	request := httptest.NewRequest(http.MethodPost, "/telegram/webhook", strings.NewReader(string(body)))
	request.Header.Set(webhookSecretHeader, "wrong")
	response := httptest.NewRecorder()
	bot.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", response.Code)
	}
}

func TestSnapshotCannotBeReadOrInvalidatedByAnotherChat(t *testing.T) {
	store := newSnapshotStore()
	id := store.create(42, testOpportunities(time.Now(), 1))
	if _, ok := store.get(id, 7); ok {
		t.Fatal("snapshot was readable by another chat")
	}
	if _, ok := store.get(id, 42); !ok {
		t.Fatal("snapshot was invalidated by another chat")
	}
}

func serveUpdate(t *testing.T, bot *Bot, secret string, incoming update) {
	t.Helper()
	body, err := json.Marshal(incoming)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/telegram/webhook", strings.NewReader(string(body)))
	request.Header.Set(webhookSecretHeader, secret)
	response := httptest.NewRecorder()
	bot.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("webhook status = %d, want 200", response.Code)
	}
}

func testOpportunities(now time.Time, count int) []domain.Opportunity {
	opportunities := make([]domain.Opportunity, count)
	for i := range opportunities {
		opportunities[i] = domain.Opportunity{
			ID:                  "opportunity",
			DetectedAt:          now.Add(-10 * time.Second),
			Asset:               "ASSET-" + strconv.Itoa(i+1),
			VenuePair:           domain.VenuePair{VenueA: "pacifica", VenueB: "hyperliquid"},
			Direction:           domain.DirectionLongA,
			FundingRateA:        -0.00001,
			FundingRateB:        0.00002,
			AnnualizedGrossEdge: 0.25,
			RecommendedNotional: 2500,
			ExecutionStatus:     "executable",
		}
	}
	return opportunities
}
