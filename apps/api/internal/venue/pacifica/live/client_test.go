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

func TestSendOrderPreservesRejectionDetails(t *testing.T) {
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
			"code": 400,
			"data": map[string]any{"message": "invalid amount precision"},
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
	if result.Accepted || !strings.Contains(result.Error, "invalid amount precision") {
		t.Fatalf("result = %+v, want rejection details", result)
	}
}

func TestSendOrderMapsPacificaLotSizeRejection(t *testing.T) {
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
			"code": 400,
			"err":  "Market order amount 45.66 is not a multiple of lot size 0.1",
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
		Symbol: "VIRTUAL", ClientOrderID: "order-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Error != "Order amount does not match Pacifica's lot size" {
		t.Fatalf("error = %q, want user-facing lot size message", result.Error)
	}
}
