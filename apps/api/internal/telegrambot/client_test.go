package telegrambot

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestClientSendsTelegramMessage(t *testing.T) {
	var method string
	var payload struct {
		ChatID    int64  `json:"chat_id"`
		Text      string `json:"text"`
		ParseMode string `json:"parse_mode"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	client := NewClient("token", server.Client())
	client.baseURL = server.URL
	if err := client.SendMessage(context.Background(), 42, "hello", InlineKeyboardMarkup{}); err != nil {
		t.Fatal(err)
	}
	if method != "/sendMessage" || payload.ChatID != 42 || payload.Text != "hello" || payload.ParseMode != "HTML" {
		t.Fatalf("request method=%q payload=%+v", method, payload)
	}
}

func TestClientTransportErrorDoesNotExposeBotToken(t *testing.T) {
	const token = "secret-bot-token"
	transportErr := errors.New("network unavailable")
	client := NewClient(token, &http.Client{Transport: roundTripFunc(
		func(*http.Request) (*http.Response, error) {
			return nil, transportErr
		},
	)})

	err := client.SendMessage(context.Background(), 42, "hello", InlineKeyboardMarkup{})
	if err == nil {
		t.Fatal("expected transport error")
	}
	if strings.Contains(err.Error(), token) {
		t.Fatalf("transport error exposed bot token: %q", err)
	}
	if !errors.Is(err, transportErr) {
		t.Fatalf("transport cause was not preserved: %v", err)
	}
}
