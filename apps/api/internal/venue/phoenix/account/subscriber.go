package account

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	defaultRESTURL  = "https://perp-api.phoenix.trade"
	defaultWSURL    = "wss://perp-api.phoenix.trade/v1/ws"
	readTimeout     = 5 * time.Second
	maxMessageBytes = 4 * 1024 * 1024
	refreshInterval = 15 * time.Second
	reconnectDelay  = 5 * time.Second
)

// Reader is shared across owner subscriptions so HTTP 429 backoff is shared.
// Only selected accounts are read; there is no market/account-wide fan-out.
type Reader struct {
	baseURL string
	http    *http.Client
	mu      sync.Mutex
	retryAt time.Time
}

func NewReader(baseURL string, client *http.Client) *Reader {
	if baseURL == "" {
		baseURL = defaultRESTURL
	}
	if client == nil {
		client = &http.Client{Timeout: readTimeout}
	}
	return &Reader{baseURL: strings.TrimRight(baseURL, "/"), http: client}
}

func (r *Reader) read(ctx context.Context, owner string) (message, time.Time, error) {
	r.mu.Lock()
	blocked := time.Now().Before(r.retryAt)
	r.mu.Unlock()
	if blocked {
		return message{}, time.Time{}, errors.New("Phoenix account reads backing off")
	}
	ctx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()
	observedAt := time.Now().UTC() // Include request latency in freshness.
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, r.baseURL+"/v1/trader/state/"+owner+"?traderPdaIndex=0", nil)
	if err != nil {
		return message{}, time.Time{}, errors.New("invalid Phoenix account endpoint")
	}
	response, err := r.http.Do(request)
	if err != nil {
		return message{}, time.Time{}, errors.New("Phoenix account request failed")
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusTooManyRequests {
		delay := reconnectDelay
		if seconds, err := strconv.ParseUint(response.Header.Get("Retry-After"), 10, 32); err == nil {
			if candidate := time.Duration(seconds) * time.Second; candidate > delay {
				delay = candidate
			}
		} else if date, err := http.ParseTime(response.Header.Get("Retry-After")); err == nil && time.Until(date) > delay {
			delay = time.Until(date)
		}
		r.mu.Lock()
		if retryAt := time.Now().Add(delay); retryAt.After(r.retryAt) {
			r.retryAt = retryAt
		}
		r.mu.Unlock()
	}
	if response.StatusCode != http.StatusOK {
		return message{}, time.Time{}, fmt.Errorf("Phoenix account HTTP %d", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxMessageBytes+1))
	if err != nil {
		return message{}, time.Time{}, errors.New("Phoenix account response read failed")
	}
	if len(raw) > maxMessageBytes {
		return message{}, time.Time{}, errors.New("Phoenix account response too large")
	}
	m, err := decodeREST(raw)
	return m, observedAt, err
}

type refresh struct {
	done chan struct{}
	err  error
}

type Subscriber struct {
	state         *State
	reader        *Reader
	logger        *slog.Logger
	wsURL         string
	dialer        *websocket.Dialer
	reconnectWait time.Duration
	refreshEvery  time.Duration
	refreshMu     sync.Mutex
	refresh       *refresh
	updates       chan struct{}
}

func NewSubscriber(state *State, reader *Reader, logger *slog.Logger) *Subscriber {
	return NewSubscriberWithURL(state, reader, logger, defaultWSURL)
}

func NewSubscriberWithURL(state *State, reader *Reader, logger *slog.Logger, wsURL string) *Subscriber {
	if logger == nil {
		logger = slog.Default()
	}
	if wsURL == "" {
		wsURL = defaultWSURL
	}
	return &Subscriber{state: state, reader: reader, logger: logger, wsURL: wsURL,
		dialer: websocket.DefaultDialer, reconnectWait: reconnectDelay, refreshEvery: refreshInterval, updates: make(chan struct{}, 1)}
}

// Updates coalesces account lifecycle/state changes for future registry consumers.
func (s *Subscriber) Updates() <-chan struct{} { return s.updates }
func (s *Subscriber) notify() {
	select {
	case s.updates <- struct{}{}:
	default:
	}
}

// Refresh shares an in-flight full read. Waiter cancellation does not cancel a
// read another waiter needs; the read itself always has a five-second deadline.
func (s *Subscriber) Refresh(ctx context.Context) error {
	if s.state == nil || s.reader == nil {
		return errors.New("Phoenix account subscriber requires state and reader")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.refreshMu.Lock()
	f := s.refresh
	if f == nil {
		f = &refresh{done: make(chan struct{})}
		s.refresh = f
		generation := s.state.Snapshot().Generation
		go func() {
			// Detach waiter cancellation; Reader still enforces its own deadline.
			m, at, err := s.reader.read(context.WithoutCancel(ctx), s.state.Snapshot().Owner)
			if err == nil {
				err = s.state.apply(generation, m, at, false)
				s.notify()
			}
			s.refreshMu.Lock()
			f.err = err
			s.refresh = nil
			close(f.done)
			s.refreshMu.Unlock()
		}()
	}
	s.refreshMu.Unlock()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-f.done:
		return f.err
	}
}

func (s *Subscriber) Run(ctx context.Context) error {
	if s.state == nil || s.reader == nil {
		return errors.New("Phoenix account subscriber requires state and reader")
	}
	for ctx.Err() == nil {
		err := s.connectAndListen(ctx)
		if ctx.Err() != nil {
			break
		}
		s.logger.Warn("phoenix account stream disconnected; resync required", "err", err)
		timer := time.NewTimer(s.reconnectWait)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
	return ctx.Err()
}

func (s *Subscriber) connectAndListen(ctx context.Context) error {
	generation := s.state.begin()
	s.notify()
	defer func() { s.state.disconnect(generation); s.notify() }()
	if err := s.Refresh(ctx); err != nil && ctx.Err() == nil {
		s.logger.Warn("phoenix account bootstrap failed; awaiting stream snapshot", "err", err)
	}
	dialCtx, cancelDial := context.WithTimeout(ctx, readTimeout)
	connection, response, err := s.dialer.DialContext(dialCtx, s.wsURL, nil)
	cancelDial()
	if response != nil && response.Body != nil {
		response.Body.Close()
	}
	if err != nil {
		return errors.New("Phoenix account websocket dial failed")
	}
	defer connection.Close()
	connection.SetReadLimit(maxMessageBytes)
	deadline := func() error { return connection.SetReadDeadline(time.Now().Add(45 * time.Second)) }
	if err := deadline(); err != nil {
		return err
	}
	connection.SetPongHandler(func(string) error { return deadline() })
	if err := connection.SetWriteDeadline(time.Now().Add(readTimeout)); err != nil {
		return err
	}
	owner := s.state.Snapshot().Owner
	if err := connection.WriteJSON(map[string]any{"type": "subscribe", "subscription": map[string]any{"channel": "traderState", "authority": owner, "traderPdaIndex": 0}}); err != nil {
		return err
	}
	s.state.connected(generation)
	connectedAt := time.Now()
	s.notify()
	type incoming struct {
		raw []byte
		err error
		at  time.Time
	}
	messages := make(chan incoming, 1)
	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			_, raw, err := connection.ReadMessage()
			at := time.Now().UTC()
			if err == nil {
				err = deadline()
			}
			select {
			case messages <- incoming{raw, err, at}:
			case <-done:
				return
			}
			if err != nil {
				return
			}
		}
	}()
	ticker := time.NewTicker(s.refreshEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case read := <-messages:
			if read.err != nil {
				return errors.New("Phoenix account websocket read failed")
			}
			var envelope struct {
				Channel string `json:"channel"`
				Type    string `json:"type"`
			}
			if err := json.Unmarshal(read.raw, &envelope); err != nil {
				return err
			}
			if envelope.Type == "subscriptionError" || envelope.Channel == "error" {
				return errors.New("Phoenix trader-state subscription rejected")
			}
			if envelope.Channel != "traderState" {
				continue
			}
			var m message
			if err := json.Unmarshal(read.raw, &m); err != nil {
				return err
			}
			if err := s.state.apply(generation, m, read.at, true); err != nil {
				return err
			}
			s.notify()
		case <-ticker.C:
			if !s.state.Snapshot().Fresh(time.Now()) && time.Since(connectedAt) > Freshness {
				return errors.New("Phoenix account stream stale; resync required")
			}
			if err := connection.WriteControl(websocket.PingMessage, nil, time.Now().Add(readTimeout)); err != nil {
				return err
			}
			if err := s.Refresh(ctx); err != nil && ctx.Err() == nil {
				s.logger.Warn("phoenix account reconciliation failed", "err", err)
			}
		}
	}
}
