package middleware

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLoggingPreservesResponseFlushing(t *testing.T) {
	var flushErr error
	handler := Logging(slog.New(slog.NewTextHandler(io.Discard, nil)))(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, ": ready\n\n")
			flushErr = http.NewResponseController(w).Flush()
		},
	))

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/events", nil))
	if flushErr != nil {
		t.Fatalf("flush through logging middleware: %v", flushErr)
	}
}
