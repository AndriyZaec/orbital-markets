package account

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type heartbeatTradeHandler struct{ trades chan json.RawMessage }

func (h *heartbeatTradeHandler) HandleOrderUpdate(json.RawMessage) {}
func (h *heartbeatTradeHandler) HandleTrade(data json.RawMessage)  { h.trades <- data }

func TestAccountHeartbeatPreventsIdleDisconnectAndDeliversTrade(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		name := "without ping"
		if enabled {
			name = "with ping"
		}
		t.Run(name, func(t *testing.T) {
			const idleTimeout = 200 * time.Millisecond
			serverRead := make(chan error, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					t.Errorf("upgrade: %v", err)
					return
				}
				defer conn.Close()
				started := time.Now()
				sent := false
				for n := 0; ; n++ {
					_ = conn.SetReadDeadline(time.Now().Add(idleTimeout))
					var msg struct {
						Method string `json:"method"`
					}
					if err := conn.ReadJSON(&msg); err != nil {
						serverRead <- err
						return
					}
					want := "subscribe"
					if n >= 6 {
						want = "ping"
					}
					if msg.Method != want {
						t.Errorf("message %d: method=%q, want %q", n, msg.Method, want)
						return
					}
					if !sent && time.Since(started) >= 2*idleTimeout {
						if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"channel":"account_trades","data":{"history_id":42}}`)); err != nil {
							t.Errorf("send trade: %v", err)
							return
						}
						sent = true
					}
				}
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			handler := &heartbeatTradeHandler{trades: make(chan json.RawMessage, 1)}
			state := NewAccountState()
			state.ResetForAccount("wallet")
			subscriber := NewSubscriber(slog.New(slog.NewTextHandler(io.Discard, nil)), state, "wallet", handler)
			subscriber.wsURL = "ws" + strings.TrimPrefix(server.URL, "http")
			subscriber.heartbeatInterval = time.Hour
			if enabled {
				subscriber.heartbeatInterval = 20 * time.Millisecond
			}
			done := make(chan error, 1)
			go func() { done <- subscriber.connectAndListen(ctx) }()
			if !enabled {
				select {
				case err := <-serverRead:
					var timeout net.Error
					if !errors.As(err, &timeout) || !timeout.Timeout() {
						t.Fatalf("server closed for %v, want idle timeout", err)
					}
				case <-ctx.Done():
					t.Fatal("server did not enforce idle timeout")
				}
			} else {
				select {
				case trade := <-handler.trades:
					if string(trade) != `{"history_id":42}` {
						t.Fatalf("unexpected trade: %s", trade)
					}
				case err := <-done:
					t.Fatalf("disconnected before trade: %v", err)
				case <-ctx.Done():
					t.Fatal("trade not delivered after idle threshold")
				}
				select {
				case err := <-done:
					t.Fatalf("connection ended before cancellation: %v", err)
				default:
				}
				cancel()
			}
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("expected connection termination error")
				}
			case <-time.After(time.Second):
				t.Fatal("listener did not exit after disconnect/cancellation")
			}
		})
	}
}
