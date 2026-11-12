package live

import (
	"context"
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

func TestTrackerHeartbeatPreventsIdleDisconnectAndDeliversFill(t *testing.T) {
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
					if n >= 2 {
						want = "ping"
					}
					if msg.Method != want {
						t.Errorf("message %d: method=%q, want %q", n, msg.Method, want)
						return
					}
					if !sent && time.Since(started) >= 2*idleTimeout {
						if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"channel":"orderUpdates","data":[{"order":{"coin":"SOL","oid":42,"origSz":"1","sz":"0","cloid":"0xorder"},"status":"filled"}]}`)); err != nil {
							t.Errorf("send fill: %v", err)
							return
						}
						sent = true
					}
				}
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			tracker := NewTracker(slog.New(slog.NewTextHandler(io.Discard, nil)), "0xaccount")
			tracker.wsURL = "ws" + strings.TrimPrefix(server.URL, "http")
			tracker.heartbeatInterval = time.Hour
			if enabled {
				tracker.heartbeatInterval = 20 * time.Millisecond
			}
			tracker.Register(&SubmitResult{OrderID: "42", ClientOrderID: "0xorder", Symbol: "SOL", SubmittedAt: time.Now()}, 1)
			done := make(chan error, 1)
			go func() { done <- tracker.connectAndListen(ctx) }()
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
				fill, err := tracker.WaitForFill(ctx, "0xorder")
				if err != nil {
					t.Fatal(err)
				}
				if fill.Status != OrderStatusFilled || fill.FilledAmount != 1 {
					t.Fatalf("fill after idle threshold = %+v", fill)
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
