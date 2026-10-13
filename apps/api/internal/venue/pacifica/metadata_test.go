package pacifica

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type metadataTransport func(*http.Request) (*http.Response, error)

func (f metadataTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func metadataResponse() *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(
		`{"success":true,"data":[{"symbol":"BTC","lot_size":"0.001","max_leverage":50}]}`,
	))}
}

func TestMetadataRefreshCoalescesAndCachesConstraints(t *testing.T) {
	a := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	var reads atomic.Int32
	var clock atomic.Int64
	clock.Store(time.Now().UnixNano())
	a.now = func() time.Time { return time.Unix(0, clock.Load()) }
	started, release := make(chan struct{}), make(chan struct{})
	a.client.Transport = metadataTransport(func(*http.Request) (*http.Response, error) {
		if reads.Add(1) == 1 {
			close(started)
			<-release
		}
		return metadataResponse(), nil
	})
	var callers sync.WaitGroup
	for range 100 {
		callers.Add(1)
		go func() {
			defer callers.Done()
			if err := a.RefreshMetadata(context.Background()); err != nil {
				t.Error(err)
			}
		}()
	}
	<-started
	close(release)
	callers.Wait()
	for range 1000 {
		if err := a.RefreshMetadata(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if reads.Load() != 1 {
		t.Fatalf("cached/concurrent metadata caused %d REST reads", reads.Load())
	}
	if lot, ok := a.LotSize("BTC"); !ok || lot != "0.001" {
		t.Fatalf("lot constraint missing: %q, %t", lot, ok)
	}
	if _, ok := a.LotSize("UNKNOWN"); ok {
		t.Fatal("unknown market acquired a lot constraint")
	}
	clock.Add(int64(metadataTTL + time.Second))
	if err := a.RefreshMetadata(context.Background()); err != nil || reads.Load() != 2 {
		t.Fatalf("expired metadata not refreshed: %v, reads=%d", err, reads.Load())
	}
}

func TestMetadataWaitHonorsDeadlineWithoutCancelingSharedRefresh(t *testing.T) {
	a := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	started, release := make(chan struct{}), make(chan struct{})
	var reads atomic.Int32
	a.client.Transport = metadataTransport(func(r *http.Request) (*http.Response, error) {
		reads.Add(1)
		close(started)
		select {
		case <-release:
			return metadataResponse(), nil
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() { first <- a.RefreshMetadata(ctx) }()
	<-started
	cancel()
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatalf("initiating caller didn't cancel: %v", err)
	}
	deadlineCtx, deadlineCancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer deadlineCancel()
	second := make(chan error, 1)
	go func() { second <- a.RefreshMetadata(deadlineCtx) }()
	select {
	case err := <-second:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("metadata waiter ignored its deadline")
	}
	close(release)
	if err := a.RefreshMetadata(context.Background()); err != nil || reads.Load() != 1 {
		t.Fatalf("caller cancellation poisoned shared refresh: %v, reads=%d", err, reads.Load())
	}
}

func TestMetadataFailuresBackOffAndRecover(t *testing.T) {
	a := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	var reads atomic.Int32
	var clock atomic.Int64
	clock.Store(time.Now().UnixNano())
	a.now = func() time.Time { return time.Unix(0, clock.Load()) }
	a.client.Transport = metadataTransport(func(*http.Request) (*http.Response, error) {
		if reads.Add(1) == 1 {
			return nil, errors.New("venue unavailable")
		}
		return metadataResponse(), nil
	})
	for range 100 {
		if err := a.RefreshMetadata(context.Background()); err == nil {
			t.Fatal("failed refresh accepted")
		}
	}
	if reads.Load() != 1 {
		t.Fatal("failed requests were not coalesced/backed off")
	}
	clock.Add(int64(metadataRetryTTL + time.Second))
	if err := a.RefreshMetadata(context.Background()); err != nil || reads.Load() != 2 {
		t.Fatalf("retry did not recover: %v", err)
	}
}
