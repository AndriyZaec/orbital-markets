package analytics

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEmitterSendsCapturePayload(t *testing.T) {
	t.Parallel()

	received := make(chan capturePayload, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
			return
		}
		var payload capturePayload
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Errorf("decode body: %v", err)
			return
		}
		received <- payload
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	emitter := NewEmitter(slog.Default(), "project-key", server.URL)
	emitter.Track("live_open_succeeded", "session:test", map[string]any{"asset": "SOL"})
	emitter.Close()

	payload := <-received
	if payload.APIKey != "project-key" || payload.Event != "live_open_succeeded" || payload.DistinctID != "session:test" {
		t.Fatalf("unexpected payload: %+v", payload)
	}
	if payload.Properties["asset"] != "SOL" {
		t.Fatalf("unexpected properties: %+v", payload.Properties)
	}
}

func TestDisabledEmitterDoesNotSend(t *testing.T) {
	t.Parallel()

	emitter := NewEmitter(slog.Default(), "", "")
	if emitter.Enabled() {
		t.Fatal("empty key should disable emitter")
	}
	emitter.Track("live_open_succeeded", "session:test", nil)
	emitter.Close()
}
