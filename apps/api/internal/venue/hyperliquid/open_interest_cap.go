package hyperliquid

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
)

func (a *Adapter) OpenBlockedAssets() map[string]string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	blocked := make(map[string]string, len(a.openBlocked))
	for asset, reason := range a.openBlocked {
		blocked[asset] = reason
	}
	return blocked
}

func (a *Adapter) OpenBlockDataReady() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return !a.openBlockedAt.IsZero() && a.now().Sub(a.openBlockedAt) <= 3*time.Minute
}

func (a *Adapter) openInterestCapLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		if err := a.pollOpenInterestCap(ctx); err != nil {
			a.logger.Warn("hyperliquid open interest cap: fetch failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (a *Adapter) pollOpenInterestCap(ctx context.Context) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, a.marketURL, strings.NewReader(`{"type":"perpsAtOpenInterestCap"}`))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := a.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Hyperliquid cap list returned HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return err
	}
	var coins []string
	if err := json.Unmarshal(body, &coins); err != nil {
		return fmt.Errorf("decode Hyperliquid cap list: %w", err)
	}
	if coins == nil {
		return fmt.Errorf("Hyperliquid cap list must be an array")
	}
	blocked := make(map[string]string, len(coins))
	for _, coin := range coins {
		if coin == "" || strings.TrimSpace(coin) != coin {
			return fmt.Errorf("invalid coin in Hyperliquid cap list")
		}
		blocked[strings.ToUpper(coin)] = venue.OpenBlockOpenInterestCap
	}
	a.mu.Lock()
	a.openBlocked, a.openBlockedAt = blocked, a.now()
	a.mu.Unlock()
	return nil
}
