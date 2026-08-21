package telegrambot

import (
	"context"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/executor"
)

type fakePositionSource struct {
	positions       []executor.LivePosition
	listPacifica    string
	listHyperliquid string
	listLimit       int
	listCalls       int
}

func (s *fakePositionSource) ListRecentActivePositionsForAccounts(
	_ context.Context,
	pacifica, hyperliquid string,
	limit int,
) ([]executor.LivePosition, error) {
	s.listCalls++
	s.listPacifica = pacifica
	s.listHyperliquid = hyperliquid
	s.listLimit = limit
	return append([]executor.LivePosition(nil), s.positions...), nil
}

func TestPositionsRequireLinkedAccounts(t *testing.T) {
	messenger := &fakeMessenger{}
	bot := newPositionTestBot(&fakeAccountLinks{}, &fakePositionSource{}, messenger)

	serveUpdate(t, bot, "secret", update{Message: &message{
		Text: "/positions",
		Chat: chat{ID: 42, Type: "private"},
	}})

	if len(messenger.sent) != 1 || !strings.Contains(messenger.sent[0].text, "Link accounts") {
		t.Fatalf("messages = %+v", messenger.sent)
	}
	if got := messenger.sent[0].keyboard.InlineKeyboard[1][0].URL; got != "https://app.example" {
		t.Fatalf("link URL = %q", got)
	}
}

func TestDashboardNavigationEditsTheExistingMessage(t *testing.T) {
	now := time.Now().UTC()
	links := linkedAccountFixture()
	source := &fakePositionSource{positions: testPositions(now, 1)}
	messenger := &fakeMessenger{}
	bot := newPositionTestBot(links, source, messenger)

	serveUpdate(t, bot, "secret", update{Message: &message{
		Text: "/opportunities",
		Chat: chat{ID: 42, Type: "private"},
	}})
	positionsCallback := messenger.sent[0].keyboard.InlineKeyboard[0][1].CallbackData
	serveUpdate(t, bot, "secret", update{CallbackQuery: &callbackQuery{
		ID:   "show-positions",
		Data: positionsCallback,
		Message: &message{
			MessageID: 9,
			Chat:      chat{ID: 42, Type: "private"},
		},
	}})

	if len(messenger.sent) != 1 || len(messenger.edited) != 1 {
		t.Fatalf("sent = %d, edited = %d", len(messenger.sent), len(messenger.edited))
	}
	if messenger.edited[0].messageID != 9 || !strings.Contains(messenger.edited[0].text, "📊 Active Positions") {
		t.Fatalf("navigation edit = %+v", messenger.edited[0])
	}
}

func TestPositionsPaginationReloadsBoundedAccountScopedHistory(t *testing.T) {
	now := time.Date(2026, time.August, 17, 12, 0, 0, 0, time.UTC)
	links := linkedAccountFixture()
	source := &fakePositionSource{positions: testPositions(now, 7)}
	messenger := &fakeMessenger{}
	bot := newPositionTestBot(links, source, messenger)
	bot.now = func() time.Time { return now }

	serveUpdate(t, bot, "secret", update{Message: &message{
		Text: "/positions",
		Chat: chat{ID: 42, Type: "private"},
	}})
	if source.listPacifica != "pacifica-owner" || source.listHyperliquid != "0xhyperliquid" || source.listLimit != 50 {
		t.Fatalf("list accounts = %q / %q, limit = %d", source.listPacifica, source.listHyperliquid, source.listLimit)
	}
	first := messenger.sent[0]
	if !strings.Contains(first.text, "1. ASSET-1") || strings.Contains(first.text, "6. ASSET-6") {
		t.Fatalf("first page text = %q", first.text)
	}
	if strings.Contains(first.text, "Funding") || !strings.Contains(first.text, "PnL") {
		t.Fatalf("position summary is not compact: %q", first.text)
	}
	nextCallback := first.keyboard.InlineKeyboard[5][1].CallbackData
	if !strings.HasPrefix(nextCallback, "positions:") || len(nextCallback) > 64 {
		t.Fatalf("next callback = %q", nextCallback)
	}

	serveUpdate(t, bot, "secret", update{CallbackQuery: &callbackQuery{
		ID:   "positions-next",
		Data: nextCallback,
		Message: &message{
			MessageID: 9,
			Chat:      chat{ID: 42, Type: "private"},
		},
	}})
	if source.listCalls != 2 {
		t.Fatalf("list calls = %d, want page reload from storage", source.listCalls)
	}
	if len(messenger.edited) != 1 || !strings.Contains(messenger.edited[0].text, "6. ASSET-6") {
		t.Fatalf("second page edits = %+v", messenger.edited)
	}
}

func TestPositionPnLUsesSignSpecificMarkers(t *testing.T) {
	now := time.Date(2026, time.August, 17, 12, 0, 0, 0, time.UTC)
	positions := testPositions(now, 3)
	positions[0].TotalPnL = 12.34
	positions[1].TotalPnL = -8.12

	text, _ := renderPositions(positions, 0, "https://app.example", now)
	for _, want := range []string{"🟢 PnL +$12.34", "🔴 PnL -$8.12", "⚪ PnL $0.00"} {
		if !strings.Contains(text, want) {
			t.Fatalf("positions text missing %q: %q", want, text)
		}
	}
}

func TestFormatAgeUsesHoursAndDays(t *testing.T) {
	now := time.Date(2026, time.August, 17, 12, 0, 0, 0, time.UTC)
	if got := formatAge(now.Add(-2*time.Hour), now); got != "2h ago" {
		t.Fatalf("two-hour age = %q, want 2h ago", got)
	}
	if got := formatAge(now.Add(-72*time.Hour), now); got != "3d ago" {
		t.Fatalf("three-day age = %q, want 3d ago", got)
	}
}

func TestPositionDetailReloadsWithinCurrentLinkedAccounts(t *testing.T) {
	now := time.Date(2026, time.August, 17, 12, 0, 0, 0, time.UTC)
	positions := testPositions(now, 1)
	positions[0].TotalPnL = 12.34
	positions[0].PricePnL = -8.12
	positions[0].FundingPnL = 4.22
	positions[0].FundingPnLSource = "realized"
	positions[0].State = "degraded"
	positions[0].HoldHours = 2.5
	positions[0].HedgeMismatch = 0.02
	positions[0].Leg1CurPrice = 100
	positions[0].Leg1LiqPrice = 80
	positions[0].Leg1LiqDist = 0.2
	positions[0].Leg1LiqRisk = "elevated"
	positions[0].Leg2CurPrice = 101
	positions[0].Leg2LiqPrice = 125
	positions[0].Leg2LiqDist = -0.03
	positions[0].Leg2LiqRisk = "critical"
	links := linkedAccountFixture()
	source := &fakePositionSource{positions: positions}
	messenger := &fakeMessenger{}
	bot := newPositionTestBot(links, source, messenger)
	bot.now = func() time.Time { return now }

	serveUpdate(t, bot, "secret", update{Message: &message{
		Text: "/positions",
		Chat: chat{ID: 42, Type: "private"},
	}})
	detailCallback := messenger.sent[0].keyboard.InlineKeyboard[0][0].CallbackData
	if len(detailCallback) > 64 {
		t.Fatalf("detail callback is %d bytes: %q", len(detailCallback), detailCallback)
	}
	serveUpdate(t, bot, "secret", update{CallbackQuery: &callbackQuery{
		ID:   "position-detail",
		Data: detailCallback,
		Message: &message{
			MessageID: 10,
			Chat:      chat{ID: 42, Type: "private"},
		},
	}})

	if source.listCalls != 2 || source.listPacifica != "pacifica-owner" || source.listHyperliquid != "0xhyperliquid" {
		t.Fatalf("detail list calls = %d for %q / %q", source.listCalls, source.listPacifica, source.listHyperliquid)
	}
	got := messenger.edited[0].text
	for _, want := range []string{"Size $2.50k · Leverage 3.0x · Held 2h 30m", "🟢 <b>PnL +$12.34</b>", "Price -$8.12", "Funding +$4.22 (realized)", "⚖️ Mismatch 2.00%", "liq $80.00", "20.0% away · Elevated", "past by 3.0% · Critical", "Exposure may be incomplete. Review this position in Orbital now."} {
		if !strings.Contains(got, want) {
			t.Fatalf("detail text missing %q: %q", want, got)
		}
	}
	if refresh := messenger.edited[0].keyboard.InlineKeyboard[2][0].CallbackData; !strings.HasPrefix(refresh, "position:") {
		t.Fatalf("detail refresh callback = %q", refresh)
	}
}

func TestRelinkingInvalidatesPositionSnapshot(t *testing.T) {
	now := time.Now().UTC()
	links := linkedAccountFixture()
	positions := testPositions(now, 1)
	source := &fakePositionSource{positions: positions}
	messenger := &fakeMessenger{}
	bot := newPositionTestBot(links, source, messenger)

	serveUpdate(t, bot, "secret", update{Message: &message{
		Text: "/positions",
		Chat: chat{ID: 42, Type: "private"},
	}})
	detailCallback := messenger.sent[0].keyboard.InlineKeyboard[0][0].CallbackData
	links.link.AccountPacifica = "different-owner"
	source.positions = nil

	serveUpdate(t, bot, "secret", update{CallbackQuery: &callbackQuery{
		ID:   "stale-detail",
		Data: detailCallback,
		Message: &message{
			MessageID: 10,
			Chat:      chat{ID: 42, Type: "private"},
		},
	}})
	if source.listCalls != 2 || source.listPacifica != "different-owner" {
		t.Fatalf("detail list calls = %d for %q, want current linked account", source.listCalls, source.listPacifica)
	}
	if !strings.Contains(messenger.edited[0].text, "Position unavailable") {
		t.Fatalf("relinked detail = %+v", messenger.edited)
	}
}

func newPositionTestBot(
	links *fakeAccountLinks,
	positions *fakePositionSource,
	messenger *fakeMessenger,
) *Bot {
	return New(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		&fakeOpportunitySource{},
		messenger,
		"secret",
		"https://app.example",
		WithAccountLinks(links),
		WithPositions(positions),
	)
}

func linkedAccountFixture() *fakeAccountLinks {
	return &fakeAccountLinks{
		linked: true,
		link: AccountLink{
			AccountPacifica:    "pacifica-owner",
			AccountHyperliquid: "0xhyperliquid",
		},
	}
}

func testPositions(now time.Time, count int) []executor.LivePosition {
	positions := make([]executor.LivePosition, count)
	for index := range positions {
		positions[index] = executor.LivePosition{
			ID:               "position-" + strconv.Itoa(index+1),
			Asset:            "ASSET-" + strconv.Itoa(index+1),
			VenueA:           "pacifica",
			VenueB:           "hyperliquid",
			State:            "open",
			Notional:         2500,
			Leverage:         3,
			CurrentSpread:    0.25,
			FundingPnLSource: "pending",
			UpdatedAt:        now.Add(-10 * time.Second).Format(time.RFC3339Nano),
		}
	}
	return positions
}
