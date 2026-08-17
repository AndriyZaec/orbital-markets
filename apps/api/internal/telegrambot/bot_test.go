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

type fakeAccountLinks struct {
	consumeToken string
	consumeChat  int64
	consumeErr   error
	link         AccountLink
	linked       bool
	linkErr      error
	unlinkedChat int64
	unlinked     bool
}

func (l *fakeAccountLinks) ConsumeLinkIntent(_ context.Context, token string, chatID int64) (AccountLink, error) {
	l.consumeToken = token
	l.consumeChat = chatID
	return AccountLink{ChatID: chatID}, l.consumeErr
}

func (l *fakeAccountLinks) Unlink(_ context.Context, chatID int64) (bool, error) {
	l.unlinkedChat = chatID
	return l.unlinked, nil
}

func (l *fakeAccountLinks) AccountLink(_ context.Context, chatID int64) (AccountLink, bool, error) {
	link := l.link
	link.ChatID = chatID
	return link, l.linked, l.linkErr
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
	if strings.Contains(first.text, "Funding:") || !strings.Contains(first.text, "↗️ Long Pacifica → ↘️ Short Hyperliquid") {
		t.Fatalf("opportunity summary is not compact: %q", first.text)
	}
	if len(first.keyboard.InlineKeyboard) != 3 {
		t.Fatalf("keyboard rows = %d, want 3", len(first.keyboard.InlineKeyboard))
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

func TestStartTokenClaimsReadOnlyAccountLink(t *testing.T) {
	links := &fakeAccountLinks{}
	messenger := &fakeMessenger{}
	bot := New(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		&fakeOpportunitySource{},
		messenger,
		"secret",
		"https://app.example",
		WithAccountLinks(links),
	)
	serveUpdate(t, bot, "secret", update{Message: &message{
		Text: "/start one-time-token",
		Chat: chat{ID: 42, Type: "private"},
	}})
	if links.consumeToken != "one-time-token" || links.consumeChat != 42 {
		t.Fatalf("consumed token = %q, chat = %d", links.consumeToken, links.consumeChat)
	}
	if len(messenger.sent) != 1 || !strings.Contains(messenger.sent[0].text, "Accounts linked") {
		t.Fatalf("messages = %+v", messenger.sent)
	}
}

func TestUnlinkUsesTelegramChatIdentity(t *testing.T) {
	links := &fakeAccountLinks{unlinked: true}
	messenger := &fakeMessenger{}
	bot := New(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		&fakeOpportunitySource{},
		messenger,
		"secret",
		"https://app.example",
		WithAccountLinks(links),
	)
	serveUpdate(t, bot, "secret", update{Message: &message{
		Text: "/unlink",
		Chat: chat{ID: 42, Type: "private"},
	}})
	if links.unlinkedChat != 42 {
		t.Fatalf("unlinked chat = %d, want 42", links.unlinkedChat)
	}
	if len(messenger.sent) != 1 || !strings.Contains(messenger.sent[0].text, "remains in this Telegram chat history") {
		t.Fatalf("unlink message = %+v", messenger.sent)
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

func TestOpportunitySnapshotsRemainGloballyBounded(t *testing.T) {
	store := newSnapshotStore()
	first := store.create(42, nil)
	second := store.create(42, nil)
	if _, ok := store.get(first, 42); !ok {
		t.Fatal("older message snapshot was invalidated")
	}
	if _, ok := store.get(second, 42); !ok {
		t.Fatal("latest snapshot for the chat is missing")
	}
	for chatID := int64(1_000); chatID < 1_000+maxOpportunitySnapshots+10; chatID++ {
		store.create(chatID, nil)
	}
	if len(store.snapshots) != maxOpportunitySnapshots {
		t.Fatalf("snapshots = %d, want cap %d", len(store.snapshots), maxOpportunitySnapshots)
	}
}

func TestWebhookIgnoresRepeatedUpdateID(t *testing.T) {
	messenger := &fakeMessenger{}
	bot := New(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		&fakeOpportunitySource{},
		messenger,
		"secret",
		"https://app.example",
	)
	incoming := update{
		UpdateID: 99,
		Message:  &message{Text: "/start", Chat: chat{ID: 42, Type: "private"}},
	}
	serveUpdate(t, bot, "secret", incoming)
	serveUpdate(t, bot, "secret", incoming)
	if len(messenger.sent) != 1 {
		t.Fatalf("sent messages = %d, want one for replayed update", len(messenger.sent))
	}
}

func TestBotRateLimitsActionsPerChat(t *testing.T) {
	now := time.Date(2026, time.August, 17, 12, 0, 0, 0, time.UTC)
	messenger := &fakeMessenger{}
	bot := New(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		&fakeOpportunitySource{},
		messenger,
		"secret",
		"https://app.example",
	)
	bot.now = func() time.Time { return now }

	for updateID := int64(1); updateID <= 4; updateID++ {
		serveUpdate(t, bot, "secret", update{
			UpdateID: updateID,
			Message:  &message{Text: "/opportunities", Chat: chat{ID: 42, Type: "private"}},
		})
	}
	if len(messenger.sent) != 3 {
		t.Fatalf("sent messages = %d, want initial burst of 3", len(messenger.sent))
	}

	now = now.Add(time.Second)
	serveUpdate(t, bot, "secret", update{
		UpdateID: 5,
		Message:  &message{Text: "/opportunities", Chat: chat{ID: 42, Type: "private"}},
	})
	if len(messenger.sent) != 4 {
		t.Fatalf("sent messages = %d, want one replenished action", len(messenger.sent))
	}
}

func TestRefreshCooldownStillAcknowledgesCallbacks(t *testing.T) {
	now := time.Date(2026, time.August, 17, 12, 0, 0, 0, time.UTC)
	messenger := &fakeMessenger{}
	bot := New(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		&fakeOpportunitySource{},
		messenger,
		"secret",
		"https://app.example",
	)
	bot.now = func() time.Time { return now }

	serveRefresh := func(updateID int64, callbackID string) {
		serveUpdate(t, bot, "secret", update{
			UpdateID: updateID,
			CallbackQuery: &callbackQuery{
				ID:      callbackID,
				Data:    "opportunities:refresh",
				Message: &message{MessageID: 11, Chat: chat{ID: 42, Type: "private"}},
			},
		})
	}
	serveRefresh(1, "refresh-1")
	serveRefresh(2, "refresh-2")
	serveRefresh(3, "refresh-3")
	serveRefresh(4, "refresh-4")
	if len(messenger.edited) != 1 {
		t.Fatalf("refresh edits = %d, want 1 during cooldown", len(messenger.edited))
	}
	if len(messenger.acknowledged) != 3 {
		t.Fatalf("acknowledged callbacks = %d, want initial burst of 3", len(messenger.acknowledged))
	}

	now = now.Add(refreshCooldown)
	serveRefresh(5, "refresh-5")
	if len(messenger.edited) != 2 {
		t.Fatalf("refresh edits = %d, want 2 after cooldown", len(messenger.edited))
	}
	if len(messenger.acknowledged) != 4 {
		t.Fatalf("acknowledged callbacks = %d, want 4 after cooldown", len(messenger.acknowledged))
	}
}

func TestUnknownMessagesAreIgnored(t *testing.T) {
	messenger := &fakeMessenger{}
	bot := New(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		&fakeOpportunitySource{},
		messenger,
		"secret",
		"https://app.example",
	)
	serveUpdate(t, bot, "secret", update{
		UpdateID: 1,
		Message:  &message{Text: "hello", Chat: chat{ID: 42, Type: "private"}},
	})
	if len(messenger.sent) != 0 {
		t.Fatalf("sent messages = %d, want unknown text ignored", len(messenger.sent))
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
