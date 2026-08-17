package telegrambot

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/executor"
)

const maxTelegramPositions = 50

func (b *Bot) sendPositions(ctx context.Context, chatID int64) error {
	link, ok, err := b.accountLink(ctx, chatID)
	if err != nil {
		return fmt.Errorf("read Telegram position accounts: %w", err)
	}
	if !ok {
		return b.messenger.SendMessage(ctx, chatID, positionsLinkRequiredText(), positionsLinkKeyboard(b.appURL))
	}
	positions, err := b.recentPositions(ctx, link)
	if err != nil {
		return fmt.Errorf("list Telegram positions: %w", err)
	}
	text, keyboard := renderPositions(positions, 0, b.appURL, b.now())
	return b.messenger.SendMessage(ctx, chatID, text, keyboard)
}

func (b *Bot) editFreshPositions(ctx context.Context, chatID, messageID int64) error {
	return b.editPositionPage(ctx, chatID, messageID, 0)
}

func (b *Bot) editPositionPage(ctx context.Context, chatID, messageID int64, page int) error {
	link, ok, err := b.accountLink(ctx, chatID)
	if err != nil {
		return fmt.Errorf("read Telegram position accounts: %w", err)
	}
	if !ok {
		return b.messenger.EditMessage(ctx, chatID, messageID, positionsLinkRequiredText(), positionsLinkKeyboard(b.appURL))
	}
	positions, err := b.recentPositions(ctx, link)
	if err != nil {
		return fmt.Errorf("list Telegram positions: %w", err)
	}
	text, keyboard := renderPositions(positions, page, b.appURL, b.now())
	return b.messenger.EditMessage(ctx, chatID, messageID, text, keyboard)
}

func (b *Bot) editPositionDetail(
	ctx context.Context,
	chatID, messageID int64,
	positionToken string,
	page int,
) error {
	link, ok, err := b.accountLink(ctx, chatID)
	if err != nil {
		return fmt.Errorf("read Telegram position accounts: %w", err)
	}
	if !ok {
		return b.messenger.EditMessage(ctx, chatID, messageID, positionsLinkRequiredText(), positionsLinkKeyboard(b.appURL))
	}
	positions, err := b.recentPositions(ctx, link)
	if err != nil {
		return fmt.Errorf("list Telegram positions for detail: %w", err)
	}
	positionID := ""
	for _, position := range positions {
		if telegramPositionToken(position.ID) == positionToken {
			positionID = position.ID
			break
		}
	}
	if positionID == "" {
		text, keyboard := renderMissingPosition(page, b.appURL)
		return b.messenger.EditMessage(ctx, chatID, messageID, text, keyboard)
	}
	position, err := b.positions.GetPositionForAccounts(
		ctx,
		positionID,
		link.AccountPacifica,
		link.AccountHyperliquid,
	)
	if errors.Is(err, sql.ErrNoRows) {
		text, keyboard := renderMissingPosition(page, b.appURL)
		return b.messenger.EditMessage(ctx, chatID, messageID, text, keyboard)
	}
	if err != nil {
		return fmt.Errorf("get Telegram position detail: %w", err)
	}
	text, keyboard := renderPositionDetail(*position, page, b.appURL, b.now())
	return b.messenger.EditMessage(ctx, chatID, messageID, text, keyboard)
}

func telegramPositionToken(positionID string) string {
	hash := sha256.Sum256([]byte(positionID))
	return base64.RawURLEncoding.EncodeToString(hash[:9])
}

func (b *Bot) accountLink(ctx context.Context, chatID int64) (AccountLink, bool, error) {
	if b.links == nil || b.positions == nil {
		return AccountLink{}, false, nil
	}
	return b.links.AccountLink(ctx, chatID)
}

func (b *Bot) recentPositions(ctx context.Context, link AccountLink) ([]executor.LivePosition, error) {
	return b.positions.ListRecentActivePositionsForAccounts(
		ctx,
		link.AccountPacifica,
		link.AccountHyperliquid,
		maxTelegramPositions,
	)
}

func positionsLinkRequiredText() string {
	return "<b>Link accounts to view positions</b>\n\nConnect both venue accounts in Orbital, then link this Telegram chat."
}

func positionsLinkKeyboard(appURL string) InlineKeyboardMarkup {
	return InlineKeyboardMarkup{InlineKeyboard: [][]InlineKeyboardButton{
		{{Text: "Open Orbital", URL: appURL}},
	}}
}
