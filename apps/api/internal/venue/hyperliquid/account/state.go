package account

import (
	"sync"
	"time"
)

// MarginSummary holds the account-level margin state from Hyperliquid.
type MarginSummary struct {
	AccountEquity    float64 `json:"account_equity"`
	TotalMarginUsed  float64 `json:"total_margin_used"`
	CrossMarginRatio float64 `json:"cross_margin_ratio"`
	AvailableBalance float64 `json:"available_balance"`
	Withdrawable     float64 `json:"withdrawable"`
}

// AssetPosition is a single open position on Hyperliquid.
type AssetPosition struct {
	Coin           string  `json:"coin"`
	Side           string  `json:"side"` // "long" or "short"
	Size           float64 `json:"size"`
	EntryPx        float64 `json:"entry_px"`
	UnrealizedPnL  float64 `json:"unrealized_pnl"`
	Leverage       float64 `json:"leverage"`
	LiquidationPx  float64 `json:"liquidation_px"`
	MarginUsed     float64 `json:"margin_used"`
}

// AccountStateSnapshot is an immutable view of Hyperliquid account state.
type AccountStateSnapshot struct {
	Margin      MarginSummary   `json:"margin"`
	Positions   []AssetPosition `json:"positions"`
	LastUpdated time.Time       `json:"last_updated"`
	Connected   bool            `json:"connected"`
}

// AccountState is the live mutable Hyperliquid account state.
type AccountState struct {
	mu          sync.RWMutex
	margin      MarginSummary
	positions   []AssetPosition
	lastUpdated time.Time
	connected   bool
}

func NewAccountState() *AccountState {
	return &AccountState{}
}

func (s *AccountState) Snapshot() AccountStateSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	positions := make([]AssetPosition, len(s.positions))
	copy(positions, s.positions)

	return AccountStateSnapshot{
		Margin:      s.margin,
		Positions:   positions,
		LastUpdated: s.lastUpdated,
		Connected:   s.connected,
	}
}

func (s *AccountState) IsFresh(maxAge time.Duration) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.connected && !s.lastUpdated.IsZero() && time.Since(s.lastUpdated) <= maxAge
}

func (s *AccountState) UpdateMargin(m MarginSummary) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.margin = m
	s.lastUpdated = time.Now()
}

func (s *AccountState) UpdatePositions(positions []AssetPosition) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.positions = positions
	s.lastUpdated = time.Now()
}

func (s *AccountState) SetConnected(connected bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.connected = connected
}
