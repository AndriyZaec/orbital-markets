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
	Coin          string  `json:"coin"`
	Side          string  `json:"side"` // "long" or "short"
	Size          float64 `json:"size"`
	EntryPx       float64 `json:"entry_px"`
	UnrealizedPnL float64 `json:"unrealized_pnl"`
	Leverage      float64 `json:"leverage"`
	LiquidationPx float64 `json:"liquidation_px"`
	MarginUsed    float64 `json:"margin_used"`
}

// AccountStateSnapshot is an immutable view of Hyperliquid account state.
type AccountStateSnapshot struct {
	Account            string          `json:"account"`
	Margin             MarginSummary   `json:"margin"`
	Positions          []AssetPosition `json:"positions"`
	PositionsUpdatedAt time.Time       `json:"positions_updated_at"`
	LastUpdated        time.Time       `json:"last_updated"`
	Connected          bool            `json:"connected"`
}

// AccountState is the live mutable Hyperliquid account state.
type AccountState struct {
	mu                 sync.RWMutex
	account            string
	margin             MarginSummary
	positions          []AssetPosition
	positionsUpdatedAt time.Time
	lastUpdated        time.Time
	connected          bool
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
		Account:            s.account,
		Margin:             s.margin,
		Positions:          positions,
		PositionsUpdatedAt: s.positionsUpdatedAt,
		LastUpdated:        s.lastUpdated,
		Connected:          s.connected,
	}
}

func (s *AccountState) IsFresh(maxAge time.Duration) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.connected && !s.lastUpdated.IsZero() && time.Since(s.lastUpdated) <= maxAge
}

func (s *AccountState) UpdateMargin(m MarginSummary) {
	s.updateMargin("", m)
}

func (s *AccountState) UpdateMarginForAccount(account string, m MarginSummary) {
	s.updateMargin(account, m)
}

func (s *AccountState) updateMargin(account string, m MarginSummary) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if account != "" && s.account != account {
		return
	}
	s.margin = m
	s.lastUpdated = time.Now()
}

func (s *AccountState) UpdatePositions(positions []AssetPosition) {
	s.updatePositions("", positions)
}

func (s *AccountState) UpdatePositionsForAccount(account string, positions []AssetPosition) {
	s.updatePositions(account, positions)
}

func (s *AccountState) updatePositions(account string, positions []AssetPosition) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if account != "" && s.account != account {
		return
	}
	s.positions = positions
	s.positionsUpdatedAt = time.Now()
	s.lastUpdated = s.positionsUpdatedAt
}

func (s *AccountState) SetConnected(connected bool) {
	s.setConnected("", connected)
}

func (s *AccountState) SetConnectedForAccount(account string, connected bool) {
	s.setConnected(account, connected)
}

func (s *AccountState) setConnected(account string, connected bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if account != "" && s.account != account {
		return
	}
	s.connected = connected
}

// Reset clears state when the connected wallet address changes.
func (s *AccountState) Reset() {
	s.ResetForAccount("")
}

func (s *AccountState) ResetForAccount(account string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.account = account
	s.margin = MarginSummary{}
	s.positions = nil
	s.positionsUpdatedAt = time.Time{}
	s.lastUpdated = time.Time{}
	s.connected = false
}
