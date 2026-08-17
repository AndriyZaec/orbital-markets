package telegrambot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type InlineKeyboardButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data,omitempty"`
	URL          string `json:"url,omitempty"`
}

type InlineKeyboardMarkup struct {
	InlineKeyboard [][]InlineKeyboardButton `json:"inline_keyboard"`
}

type Messenger interface {
	SendMessage(context.Context, int64, string, InlineKeyboardMarkup) error
	EditMessage(context.Context, int64, int64, string, InlineKeyboardMarkup) error
	AnswerCallback(context.Context, string) error
}

type Client struct {
	baseURL    string
	httpClient *http.Client
}

func NewClient(token string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &Client{
		baseURL:    "https://api.telegram.org/bot" + strings.TrimSpace(token),
		httpClient: httpClient,
	}
}

func (c *Client) SendMessage(ctx context.Context, chatID int64, text string, keyboard InlineKeyboardMarkup) error {
	return c.call(ctx, "sendMessage", map[string]any{
		"chat_id":              chatID,
		"text":                 text,
		"parse_mode":           "HTML",
		"link_preview_options": map[string]bool{"is_disabled": true},
		"reply_markup":         keyboard,
	})
}

func (c *Client) EditMessage(ctx context.Context, chatID, messageID int64, text string, keyboard InlineKeyboardMarkup) error {
	return c.call(ctx, "editMessageText", map[string]any{
		"chat_id":              chatID,
		"message_id":           messageID,
		"text":                 text,
		"parse_mode":           "HTML",
		"link_preview_options": map[string]bool{"is_disabled": true},
		"reply_markup":         keyboard,
	})
}

func (c *Client) AnswerCallback(ctx context.Context, callbackID string) error {
	return c.call(ctx, "answerCallbackQuery", map[string]string{"callback_query_id": callbackID})
}

func (c *Client) call(ctx context.Context, method string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode telegram %s: %w", method, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/"+method, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create telegram %s request: %w", method, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		var urlError *url.Error
		if errors.As(err, &urlError) {
			err = urlError.Err
		}
		return fmt.Errorf("telegram %s transport: %w", method, err)
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return fmt.Errorf("read telegram %s response: %w", method, err)
	}
	var result struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(responseBody, &result); err != nil {
		return fmt.Errorf("decode telegram %s response: %w", method, err)
	}
	if resp.StatusCode != http.StatusOK || !result.OK {
		return fmt.Errorf("telegram %s rejected: %s", method, result.Description)
	}
	return nil
}
