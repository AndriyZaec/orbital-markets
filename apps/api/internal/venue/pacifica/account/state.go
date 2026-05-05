package account

import (
	"sync"
	"time"
)

// MarginMode represents the margin mode for a symbol.
type MarginMode string

const (
	MarginModeCross    MarginMode = "cross"
	MarginModeIsolated MarginMode = "isolated"
	MarginModeUnknown  MarginMode = "unknown"
)

// SymbolConfig holds per-symbol margin/leverage configuration.
type SymbolConfig struct {
	Symbol     string     `json:"symbol"`
	Leverage   float64    `json:"leverage"`
	MarginMode MarginMode `json:"margin_mode"`
}

// AccountPosition is a single open position on Pacifica.
type AccountPosition struct {
	Symbol        string  `json:"symbol"`
	Side          string  `json:"side"` // "long" or "short"
	Size          float64 `json:"size"`
	EntryPrice    float64 `json:"entry_price"`
	MarkPrice     float64 `json:"mark_price"`
	UnrealizedPnL float64 `json:"unrealized_pnl"`
	Leverage      float64 `json:"leverage"`
	MarginUsed    float64 `json:"margin_used"`
	LiqPrice      float64 `json:"liq_price"`
}

// AccountStateSnapshot is an immutable copy of account state for safe reading.
type AccountStateSnapshot struct {
	Equity              float64                `json:"equity"`
	AvailableToSpend    float64                `json:"available_to_spend"`
	AvailableToWithdraw float64                `json:"available_to_withdraw"`
	TotalMarginUsed     float64                `json:"total_margin_used"`
	MaintenanceMargin   float64                `json:"maintenance_margin"`
	SymbolConfigs       map[string]SymbolConfig `json:"symbol_configs"`
	Positions           []AccountPosition      `json:"positions"`
	LastUpdated         time.Time              `json:"last_updated"`
	Connected           bool                   `json:"connected"`
}

// AccountState is the live account state from Pacifica private streams.
type AccountState struct {
	mu sync.RWMutex

	equity              float64
	availableToSpend    float64
	availableToWithdraw float64
	totalMarginUsed     float64
	maintenanceMargin   float64
	symbolConfigs       map[string]SymbolConfig
	positions           []AccountPosition
	lastUpdated         time.Time
	connected           bool
}

func NewAccountState() *AccountState {
	return &AccountState{
		symbolConfigs: make(map[string]SymbolConfig),
	}
}

// Snapshot returns an immutable copy of the current state.
func (s *AccountState) Snapshot() AccountStateSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	positions := make([]AccountPosition, len(s.positions))
	copy(positions, s.positions)

	configs := make(map[string]SymbolConfig, len(s.symbolConfigs))
	for k, v := range s.symbolConfigs {
		configs[k] = v
	}

	return AccountStateSnapshot{
		Equity:              s.equity,
		AvailableToSpend:    s.availableToSpend,
		AvailableToWithdraw: s.availableToWithdraw,
		TotalMarginUsed:     s.totalMarginUsed,
		MaintenanceMargin:   s.maintenanceMargin,
		SymbolConfigs:       configs,
		Positions:           positions,
		LastUpdated:         s.lastUpdated,
		Connected:           s.connected,
	}
}

// IsFresh returns true if the account state was updated recently.
func (s *AccountState) IsFresh(maxAge time.Duration) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.connected && !s.lastUpdated.IsZero() && time.Since(s.lastUpdated) <= maxAge
}

// UpdateEquity sets equity and margin fields atomically.
func (s *AccountState) UpdateEquity(
	equity, availableToSpend, availableToWithdraw,
	totalMarginUsed, maintenanceMargin float64,
) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.equity = equity
	s.availableToSpend = availableToSpend
	s.availableToWithdraw = availableToWithdraw
	s.totalMarginUsed = totalMarginUsed
	s.maintenanceMargin = maintenanceMargin
	s.lastUpdated = time.Now()
}

// UpdatePositions replaces all positions atomically.
func (s *AccountState) UpdatePositions(positions []AccountPosition) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.positions = positions
	s.lastUpdated = time.Now()
}

// UpdateSymbolConfig sets leverage/margin mode for a symbol.
func (s *AccountState) UpdateSymbolConfig(cfg SymbolConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.symbolConfigs[cfg.Symbol] = cfg
	s.lastUpdated = time.Now()
}

// SetConnected marks the account stream as connected/disconnected.
func (s *AccountState) SetConnected(connected bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.connected = connected
}
