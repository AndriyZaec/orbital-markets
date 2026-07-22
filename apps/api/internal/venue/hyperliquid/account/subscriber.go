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
			return
		case <-ticker.C:
			s.poll(ctx)
		}
	}
}

func (s *Subscriber) poll(ctx context.Context) {
	perpRaw, err := s.fetchInfo(ctx, "clearinghouseState")
	if err != nil {
		if ctx.Err() == nil {
			s.logger.Error("hl account: fetch perp state failed", "err", err)
			s.state.SetConnected(false)
		}
		return
	}
	spotRaw, err := s.fetchInfo(ctx, "spotClearinghouseState")
	if err != nil {
		if ctx.Err() == nil {
			s.logger.Error("hl account: fetch unified balance failed", "err", err)
			s.state.SetConnected(false)
		}
		return
	}

	margin, positions, err := parseAccountState(perpRaw, spotRaw)
	if err != nil {
		if ctx.Err() == nil {
			s.logger.Error("hl account: parse failed", "err", err)
			s.state.SetConnected(false)
		}
		return
	}
	s.state.UpdateMargin(margin)
	s.state.UpdatePositions(positions)
	s.state.SetConnected(true)

	if !s.firstUpdate {
		s.firstUpdate = true
		s.logger.Info("hl account: first state update",
			"equity", fmt.Sprintf("%.2f", margin.AccountEquity),
			"available", fmt.Sprintf("%.2f", margin.AvailableBalance),
			"positions", len(positions),
		)
	}
}

func (s *Subscriber) fetchInfo(ctx context.Context, requestType string) ([]byte, error) {
	body := fmt.Sprintf(`{"type":%q,"user":%q}`, requestType, s.address)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, infoURL, strings.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build %s request: %w", requestType, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", requestType, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: HTTP %d", requestType, resp.StatusCode)
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read %s response: %w", requestType, err)
	}
	return raw, nil
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
func parseAccountState(perpRaw, spotRaw []byte) (MarginSummary, []AssetPosition, error) {
	var perp struct {
		MarginSummary struct {
			AccountValue    string `json:"accountValue"`
			TotalMarginUsed string `json:"totalMarginUsed"`
			TotalRawUsd     string `json:"totalRawUsd"`
		} `json:"marginSummary"`
		CrossMarginSummary struct {
			AccountValue    string `json:"accountValue"`
			TotalMarginUsed string `json:"totalMarginUsed"`
		} `json:"crossMarginSummary"`
		AssetPositions []struct {
			Position struct {
				Coin          string          `json:"coin"`
				Szi           string          `json:"szi"` // signed size: positive=long, negative=short
				EntryPx       string          `json:"entryPx"`
				PositionValue string          `json:"positionValue"`
				UnrealizedPnl string          `json:"unrealizedPnl"`
				LiquidationPx string          `json:"liquidationPx"`
				MarginUsed    string          `json:"marginUsed"`
				Leverage      json.RawMessage `json:"leverage"`
			} `json:"position"`
		} `json:"assetPositions"`
		Withdrawable string `json:"withdrawable"`
	}

	if err := json.Unmarshal(perpRaw, &perp); err != nil {
		return MarginSummary{}, nil, fmt.Errorf("unmarshal clearinghouseState: %w", err)
	}

	var spot struct {
		Balances []struct {
			Coin  string `json:"coin"`
			Token int    `json:"token"`
			Total string `json:"total"`
			Hold  string `json:"hold"`
		} `json:"balances"`
		TokenToAvailableAfterMaintenance [][]json.RawMessage `json:"tokenToAvailableAfterMaintenance"`
	}
	if err := json.Unmarshal(spotRaw, &spot); err != nil {
		return MarginSummary{}, nil, fmt.Errorf("unmarshal spotClearinghouseState: %w", err)
	}

	accountEquity := parseFloat(perp.MarginSummary.AccountValue)
	totalMarginUsed := parseFloat(perp.MarginSummary.TotalMarginUsed)
	availableBalance := accountEquity - totalMarginUsed
	withdrawable := parseFloat(perp.Withdrawable)

	// Hyperliquid unified accounts keep trading USDC in spot state. The API
	// documents spotClearinghouseState as the source of truth across spot and
	// perps, while clearinghouseState can legitimately report zero.
	for _, balance := range spot.Balances {
		if balance.Token != 0 && balance.Coin != "USDC" {
			continue
		}
		accountEquity = parseFloat(balance.Total)
		availableBalance = accountEquity - parseFloat(balance.Hold)
		break
	}
	for _, entry := range spot.TokenToAvailableAfterMaintenance {
		if len(entry) != 2 {
			continue
		}
		var token int
		var available string
		if json.Unmarshal(entry[0], &token) == nil && token == 0 && json.Unmarshal(entry[1], &available) == nil {
			availableBalance = parseFloat(available)
			break
		}
	}
	if withdrawable == 0 && accountEquity > 0 {
		withdrawable = availableBalance
	}

	var crossMarginRatio float64
	if accountEquity > 0 {
		crossMarginRatio = totalMarginUsed / accountEquity
	}

	margin := MarginSummary{
		AccountEquity:    accountEquity,
		TotalMarginUsed:  totalMarginUsed,
		CrossMarginRatio: crossMarginRatio,
		AvailableBalance: availableBalance,
		Withdrawable:     withdrawable,
	}

	var positions []AssetPosition
	for _, ap := range perp.AssetPositions {
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

	return margin, positions, nil
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
