package telegrambot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

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
