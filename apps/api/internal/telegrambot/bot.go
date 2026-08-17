package telegrambot

import (
	"context"
	"crypto/hmac"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/executor"
)

const webhookSecretHeader = "X-Telegram-Bot-Api-Secret-Token"

const telegramUpdateDedupeSize = 4_096

type OpportunitySource interface {
	Opportunities() []domain.Opportunity
}

type AccountLinks interface {
	ConsumeLinkIntent(context.Context, string, int64) (AccountLink, error)
	AccountLink(context.Context, int64) (AccountLink, bool, error)
	Unlink(context.Context, int64) (bool, error)
}

type PositionSource interface {
	ListRecentActivePositionsForAccounts(context.Context, string, string, int) ([]executor.LivePosition, error)
	GetPositionForAccounts(context.Context, string, string, string) (*executor.LivePosition, error)
}

type Option func(*Bot)

func WithAccountLinks(links AccountLinks) Option {
	return func(bot *Bot) {
		bot.links = links
	}
}

func WithPositions(positions PositionSource) Option {
	return func(bot *Bot) {
		bot.positions = positions
	}
}

type Bot struct {
	logger        *slog.Logger
	opportunities OpportunitySource
	messenger     Messenger
	webhookSecret string
	appURL        string
	links         AccountLinks
	positions     PositionSource
	snapshots     *snapshotStore
	seenUpdates   *updateDeduper
	limits        *actionLimiter
	now           func() time.Time
}

func New(
	logger *slog.Logger,
	opportunities OpportunitySource,
	messenger Messenger,
	webhookSecret, appURL string,
	options ...Option,
) *Bot {
	bot := &Bot{
		logger:        logger,
		opportunities: opportunities,
		messenger:     messenger,
		webhookSecret: strings.TrimSpace(webhookSecret),
		appURL:        strings.TrimRight(strings.TrimSpace(appURL), "/"),
		snapshots:     newSnapshotStore(),
		seenUpdates:   newUpdateDeduper(telegramUpdateDedupeSize),
		now:           time.Now,
	}
	bot.limits = newActionLimiter(func() time.Time { return bot.now() })
	for _, option := range options {
		option(bot)
	}
	return bot
}

type update struct {
	UpdateID      int64          `json:"update_id"`
	Message       *message       `json:"message"`
	CallbackQuery *callbackQuery `json:"callback_query"`
}

type message struct {
	MessageID int64  `json:"message_id"`
	Text      string `json:"text"`
	Chat      chat   `json:"chat"`
}

type chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

type callbackQuery struct {
	ID      string   `json:"id"`
	Data    string   `json:"data"`
	Message *message `json:"message"`
}

func (b *Bot) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !hmac.Equal([]byte(r.Header.Get(webhookSecretHeader)), []byte(b.webhookSecret)) {
		http.NotFound(w, r)
		return
	}
	defer r.Body.Close()
	var incoming update
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&incoming); err != nil {
		http.Error(w, "invalid update", http.StatusBadRequest)
		return
	}
	if !b.seenUpdates.add(incoming.UpdateID) {
		w.WriteHeader(http.StatusOK)
		return
	}
	if err := b.handleUpdate(r.Context(), incoming); err != nil {
		b.logger.Error("telegram update failed", "err", err)
	}
	w.WriteHeader(http.StatusOK)
}

func (b *Bot) acknowledgeCallback(ctx context.Context, callbackID string) {
	if err := b.messenger.AnswerCallback(ctx, callbackID); err != nil {
		b.logger.Warn("telegram callback acknowledgement failed", "err", err)
	}
}

func (b *Bot) handleUpdate(ctx context.Context, incoming update) error {
	if incoming.CallbackQuery != nil {
		if incoming.CallbackQuery.Message == nil || incoming.CallbackQuery.Message.Chat.Type != "private" {
			return nil
		}
		if !b.limits.allowAction(incoming.CallbackQuery.Message.Chat.ID) {
			return nil
		}
		b.acknowledgeCallback(ctx, incoming.CallbackQuery.ID)
		return b.handleCallback(ctx, *incoming.CallbackQuery)
	}
	if incoming.Message == nil || incoming.Message.Chat.Type != "private" {
		return nil
	}
	fields := strings.Fields(incoming.Message.Text)
	if len(fields) == 0 {
		return nil
	}
	command := strings.ToLower(fields[0])
	if at := strings.IndexByte(command, '@'); at >= 0 {
		command = command[:at]
	}
	switch command {
	case "/opportunities", "/positions", "/start", "/unlink", "/help":
	default:
		return nil
	}
	if !b.limits.allowAction(incoming.Message.Chat.ID) {
		return nil
	}
	switch command {
	case "/opportunities":
		return b.sendOpportunities(ctx, incoming.Message.Chat.ID)
	case "/positions":
		return b.sendPositions(ctx, incoming.Message.Chat.ID)
	case "/start":
		if len(fields) > 1 {
			return b.claimAccountLink(ctx, incoming.Message.Chat.ID, fields[1])
		}
		return b.sendWelcome(ctx, incoming.Message.Chat.ID)
	case "/unlink":
		return b.unlinkAccount(ctx, incoming.Message.Chat.ID)
	case "/help":
		return b.sendWelcome(ctx, incoming.Message.Chat.ID)
	}
	return nil
}

func (b *Bot) claimAccountLink(ctx context.Context, chatID int64, token string) error {
	if b.links == nil {
		return b.messenger.SendMessage(ctx, chatID, "Account linking is not enabled.", mainMenuKeyboard(b.appURL))
	}
	if _, err := b.links.ConsumeLinkIntent(ctx, token, chatID); err != nil {
		if !errors.Is(err, ErrInvalidLinkToken) {
			b.logger.Error("telegram account link failed", "err", err, "chat_id", chatID)
		}
		return b.messenger.SendMessage(ctx, chatID,
			"This account link is invalid or expired. Create a new link in Orbital.",
			mainMenuKeyboard(b.appURL),
		)
	}
	return b.messenger.SendMessage(ctx, chatID,
		"<b>✅ Accounts linked</b>\n\nRead-only watchlist enabled for this chat. No trading permissions were granted.",
		mainMenuKeyboard(b.appURL),
	)
}

func (b *Bot) unlinkAccount(ctx context.Context, chatID int64) error {
	if b.links == nil {
		return b.messenger.SendMessage(ctx, chatID, "No accounts are linked.", mainMenuKeyboard(b.appURL))
	}
	unlinked, err := b.links.Unlink(ctx, chatID)
	if err != nil {
		return fmt.Errorf("unlink Telegram accounts: %w", err)
	}
	if !unlinked {
		return b.messenger.SendMessage(ctx, chatID, "No accounts are linked.", mainMenuKeyboard(b.appURL))
	}
	return b.messenger.SendMessage(ctx, chatID,
		"Accounts unlinked. Previously displayed data remains in this Telegram chat history.",
		mainMenuKeyboard(b.appURL),
	)
}

func (b *Bot) handleCallback(ctx context.Context, callback callbackQuery) error {
	if callback.Message == nil || callback.Message.Chat.Type != "private" {
		return nil
	}
	chatID := callback.Message.Chat.ID
	messageID := callback.Message.MessageID
	if callback.Data == "noop" {
		return nil
	}
	if callback.Data == "opportunities:refresh" {
		if !b.limits.allowRefresh(chatID) {
			return nil
		}
		return b.editFreshOpportunities(ctx, chatID, messageID)
	}
	if callback.Data == "positions:refresh" {
		if !b.limits.allowRefresh(chatID) {
			return nil
		}
		return b.editFreshPositions(ctx, chatID, messageID)
	}
	if strings.HasPrefix(callback.Data, "positions:") {
		page, err := strconv.Atoi(strings.TrimPrefix(callback.Data, "positions:"))
		if err != nil {
			return nil
		}
		return b.editPositionPage(ctx, chatID, messageID, page)
	}
	if strings.HasPrefix(callback.Data, "position:") {
		parts := strings.SplitN(callback.Data, ":", 3)
		if len(parts) != 3 {
			return nil
		}
		page, err := strconv.Atoi(parts[1])
		if err != nil || parts[2] == "" {
			return nil
		}
		return b.editPositionDetail(ctx, chatID, messageID, parts[2], page)
	}
	parts := strings.Split(callback.Data, ":")
	if len(parts) != 3 {
		return nil
	}
	page, err := strconv.Atoi(parts[2])
	if err != nil {
		return nil
	}
	switch parts[0] {
	case "opportunities":
		snapshot, ok := b.snapshots.get(parts[1], chatID)
		if !ok {
			return b.editFreshOpportunities(ctx, chatID, messageID)
		}
		text, keyboard := renderOpportunities(parts[1], snapshot, page, b.appURL, b.now())
		return b.messenger.EditMessage(ctx, chatID, messageID, text, keyboard)
	default:
		return nil
	}
}

func (b *Bot) sendWelcome(ctx context.Context, chatID int64) error {
	return b.messenger.SendMessage(ctx, chatID,
		"<b>Orbital Markets</b>\n\nFunding opportunities and active positions, on demand.",
		mainMenuKeyboard(b.appURL),
	)
}

func (b *Bot) sendOpportunities(ctx context.Context, chatID int64) error {
	opportunities := b.opportunities.Opportunities()
	snapshotID := b.snapshots.create(chatID, opportunities)
	snapshot, _ := b.snapshots.get(snapshotID, chatID)
	text, keyboard := renderOpportunities(snapshotID, snapshot, 0, b.appURL, b.now())
	return b.messenger.SendMessage(ctx, chatID, text, keyboard)
}

func (b *Bot) editFreshOpportunities(ctx context.Context, chatID, messageID int64) error {
	opportunities := b.opportunities.Opportunities()
	snapshotID := b.snapshots.create(chatID, opportunities)
	snapshot, _ := b.snapshots.get(snapshotID, chatID)
	text, keyboard := renderOpportunities(snapshotID, snapshot, 0, b.appURL, b.now())
	if err := b.messenger.EditMessage(ctx, chatID, messageID, text, keyboard); err != nil {
		return fmt.Errorf("refresh opportunities: %w", err)
	}
	return nil
}
