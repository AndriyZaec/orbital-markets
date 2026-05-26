package account

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	infoURL      = "https://api.hyperliquid.xyz/info"
	pollInterval = 5 * time.Second
)

// Subscriber polls Hyperliquid's REST API to keep AccountState fresh.
// Hyperliquid does not offer a WS subscription for account state,
// so we poll clearinghouseState periodically.
type Subscriber struct {
	state   *AccountState
	address string // Ethereum address (0x...)
	client  *http.Client
	logger  *slog.Logger

	firstUpdate bool // tracks whether we've logged the first successful update
}

func NewSubscriber(
	logger *slog.Logger,
	state *AccountState,
	address string,
) *Subscriber {
	return &Subscriber{
		state:   state,
		address: address,
		client:  &http.Client{Timeout: 10 * time.Second},
		logger:  logger,
	}
}

// Run polls account state until ctx is cancelled.
func (s *Subscriber) Run(ctx context.Context) {
	// Initial poll immediately
	s.poll(ctx)

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.state.SetConnected(false)
			return
		case <-ticker.C:
			s.poll(ctx)
		}
	}
}

func (s *Subscriber) poll(ctx context.Context) {
	body := fmt.Sprintf(`{"type":"clearinghouseState","user":"%s"}`, s.address)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, infoURL, strings.NewReader(body))
	if err != nil {
		s.logger.Error("hl account: build request", "err", err)
		s.state.SetConnected(false)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		s.logger.Error("hl account: fetch failed", "err", err)
		s.state.SetConnected(false)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		s.logger.Error("hl account: non-200 response", "status", resp.StatusCode)
		s.state.SetConnected(false)
		return
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		s.logger.Error("hl account: read body", "err", err)
		s.state.SetConnected(false)
		return
	}

	if err := s.parseAndUpdate(raw); err != nil {
		s.logger.Error("hl account: parse failed", "err", err)
		s.state.SetConnected(false)
		return
	}

	s.state.SetConnected(true)

	if !s.firstUpdate {
		s.firstUpdate = true
		snap := s.state.Snapshot()
		s.logger.Info("hl account: first state update",
			"equity", fmt.Sprintf("%.2f", snap.Margin.AccountEquity),
			"available", fmt.Sprintf("%.2f", snap.Margin.AvailableBalance),
			"positions", len(snap.Positions),
		)
	}
}

// clearinghouseState response shape (subset we care about):
//
//	{
//	  "marginSummary": {
//	    "accountValue": "12345.67",
//	    "totalMarginUsed": "500.00",
//	    "totalNtlPos": "5000.00",
//	    "totalRawUsd": "12345.67",
//	    "withdrawable": "11000.00"
//	  },
//	  "crossMarginSummary": {
//	    "accountValue": "12345.67",
//	    "totalMarginUsed": "500.00",
//	    "totalNtlPos": "5000.00",
//	    "totalRawUsd": "12345.67"
//	  },
//	  "assetPositions": [
//	    {
//	      "position": {
//	        "coin": "ETH",
//	        "szi": "0.5",
//	        "entryPx": "1800.0",
//	        "positionValue": "900.0",
//	        "unrealizedPnl": "50.0",
//	        "leverage": { "type": "cross", "value": 5 },
//	        "liquidationPx": "1500.0",
//	        "marginUsed": "180.0"
//	      },
//	      "type": "oneWay"
//	    }
//	  ]
//	}
func (s *Subscriber) parseAndUpdate(raw []byte) error {
	var resp struct {
		MarginSummary struct {
			AccountValue    string `json:"accountValue"`
			TotalMarginUsed string `json:"totalMarginUsed"`
			TotalRawUsd     string `json:"totalRawUsd"`
			Withdrawable    string `json:"withdrawable"`
		} `json:"marginSummary"`
		CrossMarginSummary struct {
			AccountValue    string `json:"accountValue"`
			TotalMarginUsed string `json:"totalMarginUsed"`
		} `json:"crossMarginSummary"`
		AssetPositions []struct {
			Position struct {
				Coin            string `json:"coin"`
				Szi             string `json:"szi"` // signed size: positive=long, negative=short
				EntryPx         string `json:"entryPx"`
				PositionValue   string `json:"positionValue"`
				UnrealizedPnl   string `json:"unrealizedPnl"`
				LiquidationPx   string `json:"liquidationPx"`
				MarginUsed      string `json:"marginUsed"`
				Leverage        json.RawMessage `json:"leverage"`
			} `json:"position"`
		} `json:"assetPositions"`
	}

	if err := json.Unmarshal(raw, &resp); err != nil {
		return fmt.Errorf("unmarshal clearinghouseState: %w", err)
	}

	// Update margin
	accountEquity := parseFloat(resp.MarginSummary.AccountValue)
	totalMarginUsed := parseFloat(resp.MarginSummary.TotalMarginUsed)
	withdrawable := parseFloat(resp.MarginSummary.Withdrawable)

	// Available balance = account value - margin used
	availableBalance := accountEquity - totalMarginUsed

	// Cross margin ratio
	var crossMarginRatio float64
	if accountEquity > 0 {
		crossMarginRatio = totalMarginUsed / accountEquity
	}

	s.state.UpdateMargin(MarginSummary{
		AccountEquity:    accountEquity,
		TotalMarginUsed:  totalMarginUsed,
		CrossMarginRatio: crossMarginRatio,
		AvailableBalance: availableBalance,
		Withdrawable:     withdrawable,
	})

	// Update positions
	var positions []AssetPosition
	for _, ap := range resp.AssetPositions {
		p := ap.Position
		szi := parseFloat(p.Szi)
		if szi == 0 {
			continue // skip empty positions
		}

		side := "long"
		size := szi
		if szi < 0 {
			side = "short"
			size = -szi
		}

		leverage := parseLeverage(p.Leverage)

		positions = append(positions, AssetPosition{
			Coin:          p.Coin,
			Side:          side,
			Size:          size,
			EntryPx:       parseFloat(p.EntryPx),
			UnrealizedPnL: parseFloat(p.UnrealizedPnl),
			Leverage:      leverage,
			LiquidationPx: parseFloat(p.LiquidationPx),
			MarginUsed:    parseFloat(p.MarginUsed),
		})
	}

	s.state.UpdatePositions(positions)

	return nil
}

// parseLeverage handles the HL leverage field which can be:
// {"type": "cross", "value": 5}  or  {"type": "isolated", "value": 3}
func parseLeverage(raw json.RawMessage) float64 {
	if len(raw) == 0 {
		return 1
	}
	var lev struct {
		Value float64 `json:"value"`
	}
	if err := json.Unmarshal(raw, &lev); err != nil {
		return 1
	}
	if lev.Value <= 0 {
		return 1
	}
	return lev.Value
}

func parseFloat(s string) float64 {
	f, _ := strconv.ParseFloat(s, 64)
	return f
}
