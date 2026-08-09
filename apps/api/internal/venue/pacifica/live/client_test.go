package live

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

func TestSendOrderDoesNotRetainTradingConnection(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()
		if _, _, err := conn.ReadMessage(); err != nil {
			t.Errorf("read order: %v", err)
			return
		}
		if err := conn.WriteJSON(map[string]any{
			"code": 200,
			"data": map[string]any{"I": "order-1", "i": 123, "s": "SOL"},
		}); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	defer server.Close()

	conn, _, err := websocket.DefaultDialer.Dial(
		"ws"+strings.TrimPrefix(server.URL, "http"), nil,
	)
	if err != nil {
		t.Fatal(err)
	}

	client := NewClient(slog.New(slog.NewTextHandler(io.Discard, nil)), nil, nil)
	client.conn = conn
	result, err := client.sendOrder(context.Background(), MarketOrderRequest{
		Symbol: "SOL", ClientOrderID: "order-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Accepted {
		t.Fatalf("result = %+v, want accepted", result)
	}
	if client.conn != nil {
		t.Fatal("completed submission retained an idle trading connection")
	}
}
