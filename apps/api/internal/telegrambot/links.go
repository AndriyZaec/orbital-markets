package telegrambot

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"
)

const linkIntentTTL = 10 * time.Minute
const linkIntentRateWindow = time.Second
const maxLinkIntentsPerWindow = 5

var ErrInvalidLinkToken = errors.New("invalid or expired Telegram link token")
var ErrLinkRateLimited = errors.New("Telegram link creation is temporarily rate limited")

type AccountLink struct {
	ChatID             int64
	AccountPacifica    string
	AccountHyperliquid string
	LinkedAt           time.Time
}

type LinkService struct {
	db           *sql.DB
	botUsername  string
	intentMu     sync.Mutex
	intentWindow time.Time
	intentCount  int
	now          func() time.Time
}

func NewLinkService(database *sql.DB, botUsername string) *LinkService {
	return &LinkService{
		db:          database,
		botUsername: strings.TrimPrefix(strings.TrimSpace(botUsername), "@"),
		now:         time.Now,
	}
}

func (s *LinkService) CreateLinkIntent(
	ctx context.Context,
	accountPacifica, accountHyperliquid string,
) (string, time.Time, error) {
	accountPacifica = strings.TrimSpace(accountPacifica)
	accountHyperliquid = strings.ToLower(strings.TrimSpace(accountHyperliquid))
	if accountPacifica == "" || accountHyperliquid == "" {
		return "", time.Time{}, errors.New("both venue accounts are required")
	}
	if s.botUsername == "" {
		return "", time.Time{}, errors.New("Telegram bot username is not configured")
	}
	clockNow := s.now()
	if !s.allowLinkIntent(clockNow) {
		return "", time.Time{}, ErrLinkRateLimited
	}
	now := clockNow.UTC()

	token, err := randomLinkToken()
	if err != nil {
		return "", time.Time{}, err
	}
	expiresAt := now.Add(linkIntentTTL)
	if _, err := s.db.ExecContext(ctx, `DELETE FROM telegram_link_intents WHERE expires_at <= ?`, now.Unix()); err != nil {
		return "", time.Time{}, fmt.Errorf("clean expired Telegram link intents: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO telegram_link_intents (
			token_hash, account_pacifica, account_hyperliquid, expires_at, created_at
		) VALUES (?, ?, ?, ?, ?)`,
		hashLinkToken(token), accountPacifica, accountHyperliquid, expiresAt.Unix(), now.Unix(),
	); err != nil {
		return "", time.Time{}, fmt.Errorf("create Telegram link intent: %w", err)
	}

	deepLink := url.URL{
		Scheme:   "https",
		Host:     "t.me",
		Path:     "/" + s.botUsername,
		RawQuery: url.Values{"start": []string{token}}.Encode(),
	}
	return deepLink.String(), expiresAt, nil
}

func (s *LinkService) allowLinkIntent(now time.Time) bool {
	s.intentMu.Lock()
	defer s.intentMu.Unlock()
	elapsed := now.Sub(s.intentWindow)
	if s.intentWindow.IsZero() || elapsed < 0 || elapsed >= linkIntentRateWindow {
		s.intentWindow = now
		s.intentCount = 0
	}
	if s.intentCount >= maxLinkIntentsPerWindow {
		return false
	}
	s.intentCount++
	return true
}

func (s *LinkService) ConsumeLinkIntent(ctx context.Context, token string, chatID int64) (AccountLink, error) {
	token = strings.TrimSpace(token)
	if token == "" || chatID == 0 {
		return AccountLink{}, ErrInvalidLinkToken
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AccountLink{}, fmt.Errorf("begin Telegram link transaction: %w", err)
	}
	defer tx.Rollback()

	now := s.now().UTC()
	var link AccountLink
	err = tx.QueryRowContext(ctx, `
		SELECT account_pacifica, account_hyperliquid
		FROM telegram_link_intents
		WHERE token_hash = ? AND expires_at > ?`,
		hashLinkToken(token), now.Unix(),
	).Scan(&link.AccountPacifica, &link.AccountHyperliquid)
	if errors.Is(err, sql.ErrNoRows) {
		return AccountLink{}, ErrInvalidLinkToken
	}
	if err != nil {
		return AccountLink{}, fmt.Errorf("read Telegram link intent: %w", err)
	}

	result, err := tx.ExecContext(ctx, `DELETE FROM telegram_link_intents WHERE token_hash = ?`, hashLinkToken(token))
	if err != nil {
		return AccountLink{}, fmt.Errorf("consume Telegram link intent: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		return AccountLink{}, ErrInvalidLinkToken
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO telegram_account_links (
			chat_id, account_pacifica, account_hyperliquid, linked_at
		) VALUES (?, ?, ?, ?)
		ON CONFLICT (chat_id) DO UPDATE SET
			account_pacifica = excluded.account_pacifica,
			account_hyperliquid = excluded.account_hyperliquid,
			linked_at = excluded.linked_at`,
		chatID, link.AccountPacifica, link.AccountHyperliquid, now.Unix(),
	); err != nil {
		return AccountLink{}, fmt.Errorf("save Telegram account link: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return AccountLink{}, fmt.Errorf("commit Telegram account link: %w", err)
	}
	link.ChatID = chatID
	link.LinkedAt = now
	return link, nil
}

func (s *LinkService) AccountLink(ctx context.Context, chatID int64) (AccountLink, bool, error) {
	var link AccountLink
	var linkedAt int64
	err := s.db.QueryRowContext(ctx, `
		SELECT chat_id, account_pacifica, account_hyperliquid, linked_at
		FROM telegram_account_links
		WHERE chat_id = ?`, chatID,
	).Scan(&link.ChatID, &link.AccountPacifica, &link.AccountHyperliquid, &linkedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return AccountLink{}, false, nil
	}
	if err != nil {
		return AccountLink{}, false, fmt.Errorf("read Telegram account link: %w", err)
	}
	link.LinkedAt = time.Unix(linkedAt, 0).UTC()
	return link, true, nil
}

func (s *LinkService) Unlink(ctx context.Context, chatID int64) (bool, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM telegram_account_links WHERE chat_id = ?`, chatID)
	if err != nil {
		return false, fmt.Errorf("delete Telegram account link: %w", err)
	}
	affected, err := result.RowsAffected()
	return affected > 0, err
}

func randomLinkToken() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate Telegram link token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

func hashLinkToken(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}
