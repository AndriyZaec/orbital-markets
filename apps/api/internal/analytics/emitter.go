package analytics

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	defaultHost   = "https://eu.i.posthog.com"
	queueCapacity = 64
)

func NotionalBucket(notional float64) string {
	switch {
	case notional < 1_000:
		return "under_1k"
	case notional < 10_000:
		return "1k_10k"
	case notional < 100_000:
		return "10k_100k"
	default:
		return "100k_plus"
	}
}

type captureRequest struct {
	Event      string
	DistinctID string
	Properties map[string]any
}

type capturePayload struct {
	APIKey     string         `json:"api_key"`
	Event      string         `json:"event"`
	DistinctID string         `json:"distinct_id"`
	Properties map[string]any `json:"properties"`
}

// Emitter sends low-volume product milestones without making them part of the
// trading request path. Events are best-effort and are dropped when the queue
// is full or the remote sink is unavailable.
type Emitter struct {
	logger   *slog.Logger
	apiKey   string
	endpoint string
	client   *http.Client
	queue    chan captureRequest
	wg       sync.WaitGroup
}

func NewEmitter(logger *slog.Logger, apiKey, host string) *Emitter {
	apiKey = strings.TrimSpace(apiKey)
	host = strings.TrimRight(strings.TrimSpace(host), "/")
	if host == "" {
		host = defaultHost
	}

	e := &Emitter{
		logger:   logger,
		apiKey:   apiKey,
		endpoint: host + "/capture/",
		client:   &http.Client{Timeout: 5 * time.Second},
	}
	if apiKey == "" {
		return e
	}

	e.queue = make(chan captureRequest, queueCapacity)
	e.wg.Add(1)
	go e.run()
	return e
}

func (e *Emitter) Enabled() bool {
	return e != nil && e.apiKey != "" && e.queue != nil
}

// Track is non-blocking. The caller must not depend on analytics delivery.
func (e *Emitter) Track(event, distinctID string, properties map[string]any) {
	if !e.Enabled() || event == "" || distinctID == "" {
		return
	}
	request := captureRequest{
		Event:      event,
		DistinctID: distinctID,
		Properties: properties,
	}
	select {
	case e.queue <- request:
	default:
		if e.logger != nil {
			e.logger.Warn("product analytics queue full", "event", event)
		}
	}
}

func (e *Emitter) Close() {
	if !e.Enabled() {
		return
	}
	close(e.queue)
	e.wg.Wait()
}

func (e *Emitter) run() {
	defer e.wg.Done()
	for request := range e.queue {
		if err := e.send(request); err != nil && e.logger != nil {
			e.logger.Warn("product analytics delivery failed", "event", request.Event, "err", err)
		}
	}
}

func (e *Emitter) send(request captureRequest) error {
	body, err := json.Marshal(capturePayload{
		APIKey:     e.apiKey,
		Event:      request.Event,
		DistinctID: request.DistinctID,
		Properties: request.Properties,
	})
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}

	resp, err := e.client.Post(e.endpoint, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("unexpected status: %s", resp.Status)
	}
	return nil
}
