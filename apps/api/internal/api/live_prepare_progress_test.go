package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/api/middleware"
)

func TestPrepareProgressPreservesResultStatusAndReasons(t *testing.T) {
	for _, stream := range []bool{false, true} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/v1/live/prepare", nil)
		if stream {
			r.Header.Set("Accept", "application/x-ndjson")
		}
		var logs bytes.Buffer
		p := newLivePrepareResponse(w, r, slog.New(slog.NewTextHandler(&logs, nil)))
		p.Stage("quotes")
		p.Stage("accounts")
		writeJSON(p, http.StatusConflict, map[string]any{"error": "account state not ready", "reasons": []string{"Pacifica: waiting for account snapshot"}})
		p.Finish()
		if !strings.Contains(logs.String(), "status=409") || !strings.Contains(logs.String(), "stage=quotes") || !strings.Contains(logs.String(), "stage=accounts") {
			t.Fatalf("missing stage/result timings: %s", logs.String())
		}
		if !stream {
			if w.Code != http.StatusConflict || w.Header().Get("Content-Type") != "application/json" {
				t.Fatalf("legacy response changed: %d, %s", w.Code, w.Body.String())
			}
			continue
		}
		if !w.Flushed || w.Header().Get("Content-Type") != "application/x-ndjson" {
			t.Fatal("progress not flushed")
		}
		decoder := json.NewDecoder(w.Body)
		for _, stage := range []string{"quotes", "accounts"} {
			var event struct{ Type, Stage string }
			if err := decoder.Decode(&event); err != nil || event.Type != "progress" || event.Stage != stage {
				t.Fatalf("invalid progress: %+v, %v", event, err)
			}
		}
		var result struct {
			Type   string
			Status int
			Body   struct{ Reasons []string }
		}
		if err := decoder.Decode(&result); err != nil || result.Type != "result" || result.Status != http.StatusConflict || len(result.Body.Reasons) != 1 {
			t.Fatalf("result status/reasons lost: %+v, %v", result, err)
		}
	}
}

func TestPrepareDeadlineReturnsActionableTimeout(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	w := httptest.NewRecorder()
	p := newLivePrepareResponse(w, httptest.NewRequest(http.MethodPost, "/", nil).WithContext(ctx), slog.New(slog.NewTextHandler(io.Discard, nil)))
	writeJSON(p, http.StatusUnprocessableEntity, map[string]string{"error": "refresh metadata: context deadline exceeded"})
	if w.Code != http.StatusGatewayTimeout || !strings.Contains(w.Body.String(), "PREPARE_TIMEOUT") {
		t.Fatalf("deadline remained opaque: %d %s", w.Code, w.Body.String())
	}
}

func TestPrepareProgressArrivesBeforeCompletionThroughLoggingMiddleware(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	release := make(chan struct{})
	server := httptest.NewServer(middleware.Logging(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := newLivePrepareResponse(w, r, logger)
		defer p.Finish()
		p.Stage("quotes")
		<-release
		writeJSON(p, http.StatusOK, map[string]string{"session_id": "prepared"})
	})))
	defer server.Close()
	defer close(release)
	request, _ := http.NewRequest(http.MethodPost, server.URL, nil)
	request.Header.Set("Accept", "application/x-ndjson")
	client := &http.Client{Timeout: time.Second}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var event struct{ Type, Stage string }
	if err := json.NewDecoder(response.Body).Decode(&event); err != nil || event.Type != "progress" || event.Stage != "quotes" {
		t.Fatalf("progress was buffered until result: %+v, %v", event, err)
	}
}

func TestPrepareAccountLockCancelsAndReleasesEarlierLocks(t *testing.T) {
	first := &accountFeedEntry{key: accountFeedKey{venue: "aster", account: "a"}}
	second := &accountFeedEntry{key: accountFeedKey{venue: "hyperliquid", account: "b"}}
	second.operation.Lock()
	defer second.operation.Unlock()
	accounts := &liveAccountContext{leases: map[string]*accountFeedLease{
		"aster": {entry: first}, "hyperliquid": {entry: second},
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		unlock, err := accounts.LockContext(ctx)
		if unlock != nil {
			unlock()
		}
		finished <- err
	}()
	select {
	case err := <-finished:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("prepare ignored deadline waiting for account operation")
	}
	if !first.operation.TryLock() {
		t.Fatal("cancellation leaked the first account lock")
	}
	first.operation.Unlock()
}
