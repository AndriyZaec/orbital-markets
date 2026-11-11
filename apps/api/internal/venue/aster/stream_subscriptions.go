package aster

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// Aster Futures V3: 200 streams/connection, 10 incoming messages/second.
// Reserve one stream for markPrice; commands use <=5 messages/second, leaving
// headroom for automatic pong replies. The 50-stream batch is a local bound.
const (
	maxStreamsPerConnection = 200
	bookSubscribeBatch      = 50
	subscribeCommandPace    = 200 * time.Millisecond
	subscribeTimeout        = 5 * time.Second
)

type streamSubscriptionAck struct {
	ID     uint64 `json:"id"`
	Code   *int   `json:"code"`
	Msg    string `json:"msg"`
	Result any    `json:"result"`
}

func isBookTickerStream(stream string) bool {
	return stream == "!bookTicker" || strings.HasSuffix(stream, "@bookTicker")
}

func (a *Adapter) bookSubscriptionURL() (string, bool, <-chan struct{}, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.subscriptionUpdates == nil {
		a.subscriptionUpdates = make(chan struct{}, 1)
	}
	allBooks := a.crossVenueAssets == nil
	endpoint, err := url.Parse(a.wsURL)
	if err != nil {
		return "", false, nil, err
	}
	if !allBooks {
		query := endpoint.Query()
		query.Set("streams", "!markPrice@arr@1s")
		endpoint.RawQuery = query.Encode()
	}
	return endpoint.String(), allBooks, a.subscriptionUpdates, nil
}

func (a *Adapter) demandedBookStreams() ([]string, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.crossVenueAssets == nil {
		return nil, false
	}
	streams := make([]string, 0)
	for symbol, market := range a.markets {
		if _, ok := a.crossVenueAssets[strings.ToUpper(strings.TrimSpace(market.asset))]; ok {
			streams = append(streams, strings.ToLower(symbol)+"@bookTicker")
		}
	}
	sort.Strings(streams)
	return streams, true
}

// One writer, one acknowledged request in flight. New demand is coalesced; only
// the initial all-market subscription is removed. Per-symbol subscriptions grow.
func (a *Adapter) maintainBookSubscriptions(ctx context.Context, conn *websocket.Conn, allBooks bool, updates <-chan struct{}, acks <-chan streamSubscriptionAck, openShard func([]string) (*websocket.Conn, <-chan streamSubscriptionAck, error)) error {
	subscribed := make(map[string]struct{})
	current, currentAcks, used := conn, acks, 1 // primary reserves markPrice
	var id uint64
	var lastCommand time.Time
	send := func(method string, streams []string) error {
		timer := time.NewTimer(time.Until(lastCommand.Add(subscribeCommandPace)))
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
		id++
		if err := current.SetWriteDeadline(time.Now().Add(subscribeTimeout)); err != nil {
			return err
		}
		lastCommand = time.Now()
		if err := current.WriteJSON(struct {
			Method string   `json:"method"`
			Params []string `json:"params"`
			ID     uint64   `json:"id"`
		}{method, streams, id}); err != nil {
			return fmt.Errorf("write Aster %s: %w", method, err)
		}
		deadline := time.NewTimer(subscribeTimeout)
		defer deadline.Stop()
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-deadline.C:
				return fmt.Errorf("Aster %s acknowledgement timed out", method)
			case ack := <-currentAcks:
				if ack.ID != id {
					continue
				}
				if ack.Code != nil || ack.Result != nil {
					return fmt.Errorf("Aster %s rejected: %s", method, ack.Msg)
				}
				return nil
			}
		}
	}
	for {
		streams, ready := a.demandedBookStreams()
		if ready {
			pending := make([]string, 0)
			for _, stream := range streams {
				if _, found := subscribed[stream]; !found {
					pending = append(pending, stream)
				}
			}
			if allBooks {
				if err := send("UNSUBSCRIBE", []string{"!bookTicker"}); err != nil {
					return err
				}
				allBooks = false
			}
			for len(pending) > 0 {
				n := min(len(pending), bookSubscribeBatch)
				if used == maxStreamsPerConnection {
					var err error
					current, currentAcks, err = openShard(pending[:n])
					if err != nil {
						return err
					}
					used = n
					for _, stream := range pending[:n] {
						subscribed[stream] = struct{}{}
					}
					pending = pending[n:]
					continue
				}
				n = min(n, maxStreamsPerConnection-used)
				batch := pending[:n]
				if err := send("SUBSCRIBE", batch); err != nil {
					return err
				}
				for _, stream := range batch {
					subscribed[stream] = struct{}{}
				}
				used += n
				pending = pending[n:]
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-updates:
		}
	}
}

func (a *Adapter) bookShardURL(streams []string) (string, error) {
	endpoint, err := url.Parse(a.wsURL)
	if err != nil {
		return "", err
	}
	query := endpoint.Query()
	query.Set("streams", strings.Join(streams, "/"))
	endpoint.RawQuery = query.Encode()
	return endpoint.String(), nil
}
