package account

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

const browserHeartbeatTTL = 45 * time.Second

type AccountStateSnapshot struct {
	Account            string
	Agent              string
	SnapshotID         string
	OneWayModeKnown    bool
	OneWayMode         bool
	CanTradeKnown      bool
	CanTrade           bool
	Equity             float64
	Available          float64
	Positions          []Position
	LeverageBySymbol   map[string]float64
	LeverageBrackets   LeverageBrackets
	PositionsUpdatedAt time.Time
	LastUpdated        time.Time
	Connected          bool
}

type AccountState struct {
	mu                 sync.RWMutex
	account            string
	agent              string
	snapshotID         string
	snapshotCreatedAt  time.Time
	mode               *PositionMode
	margin             *MarginSummary
	positions          *[]Position
	modeUpdatedAt      time.Time
	marginUpdatedAt    time.Time
	positionsUpdatedAt time.Time
	leverageBySymbol   map[string]float64
	leverageBrackets   LeverageBrackets
}

func NewAccountState(account string) *AccountState {
	return &AccountState{
		account:          strings.ToLower(strings.TrimSpace(account)),
		leverageBySymbol: make(map[string]float64),
		leverageBrackets: make(LeverageBrackets),
	}
}

func (s *AccountState) ApplySnapshotPart(
	account, agent, snapshotID string,
	createdAt, respondedAt time.Time,
	part SnapshotPart,
) error {
	account = strings.ToLower(strings.TrimSpace(account))
	agent = strings.ToLower(strings.TrimSpace(agent))
	if account == "" || account != s.account || agent == "" || snapshotID == "" || createdAt.IsZero() || respondedAt.Before(createdAt) {
		return fmt.Errorf("invalid Aster account snapshot context")
	}
	parts := 0
	if part.Mode != nil {
		parts++
	}
	if part.Margin != nil {
		parts++
	}
	if part.Positions != nil {
		parts++
	}
	if parts != 1 {
		return fmt.Errorf("Aster account snapshot must contain one part")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if snapshotID != s.snapshotID {
		if !s.snapshotCreatedAt.IsZero() && !createdAt.After(s.snapshotCreatedAt) {
			return nil
		}
		s.agent = agent
		s.snapshotID = snapshotID
		s.snapshotCreatedAt = createdAt
		s.mode = nil
		s.margin = nil
		s.positions = nil
		s.modeUpdatedAt = time.Time{}
		s.marginUpdatedAt = time.Time{}
		s.positionsUpdatedAt = time.Time{}
	} else if agent != s.agent || !createdAt.Equal(s.snapshotCreatedAt) {
		return fmt.Errorf("Aster account snapshot generation mismatch")
	}
	if part.Mode != nil {
		mode := *part.Mode
		s.mode = &mode
		s.modeUpdatedAt = respondedAt
	}
	if part.Margin != nil {
		margin := *part.Margin
		s.margin = &margin
		s.marginUpdatedAt = respondedAt
	}
	if part.Positions != nil {
		positions := append([]Position(nil), (*part.Positions)...)
		s.positions = &positions
		s.positionsUpdatedAt = respondedAt
		for _, position := range positions {
			s.leverageBySymbol[position.Symbol] = position.Leverage
		}
	}
	return nil
}

func (s *AccountState) ApplyLeverage(update LeverageUpdate) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.leverageBySymbol[update.Symbol] = update.Leverage
}

func (s *AccountState) ApplyLeverageBrackets(brackets LeverageBrackets) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for symbol, tiers := range brackets {
		s.leverageBrackets[symbol] = append([]LeverageBracket(nil), tiers...)
	}
}

func (s *AccountState) Snapshot() AccountStateSnapshot {
	return s.snapshotAt(time.Now())
}

func (s *AccountState) snapshotAt(now time.Time) AccountStateSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snapshot := AccountStateSnapshot{
		Account: s.account, Agent: s.agent, SnapshotID: s.snapshotID,
		LeverageBySymbol:   copyMap(s.leverageBySymbol),
		LeverageBrackets:   copyBrackets(s.leverageBrackets),
		PositionsUpdatedAt: s.positionsUpdatedAt,
	}
	if s.mode != nil {
		snapshot.OneWayModeKnown = true
		snapshot.OneWayMode = s.mode.OneWay
	}
	if s.margin != nil {
		snapshot.CanTradeKnown = true
		snapshot.CanTrade = s.margin.CanTrade
		snapshot.Equity = s.margin.Equity
		snapshot.Available = s.margin.Available
	}
	if s.positions != nil {
		snapshot.Positions = append([]Position(nil), (*s.positions)...)
	}
	if s.mode != nil && s.margin != nil && s.positions != nil {
		snapshot.LastUpdated = oldest(s.modeUpdatedAt, s.marginUpdatedAt, s.positionsUpdatedAt)
		newestUpdate := newest(s.modeUpdatedAt, s.marginUpdatedAt, s.positionsUpdatedAt)
		snapshot.Connected = !snapshot.LastUpdated.IsZero() && newestUpdate.Sub(snapshot.LastUpdated) <= 30*time.Second &&
			now.Sub(newestUpdate) <= browserHeartbeatTTL
	}
	return snapshot
}

func copyMap(source map[string]float64) map[string]float64 {
	result := make(map[string]float64, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func copyBrackets(source LeverageBrackets) LeverageBrackets {
	result := make(LeverageBrackets, len(source))
	for symbol, brackets := range source {
		result[symbol] = append([]LeverageBracket(nil), brackets...)
	}
	return result
}

func oldest(values ...time.Time) time.Time {
	result := values[0]
	for _, value := range values[1:] {
		if value.Before(result) {
			result = value
		}
	}
	return result
}

func newest(values ...time.Time) time.Time {
	result := values[0]
	for _, value := range values[1:] {
		if value.After(result) {
			result = value
		}
	}
	return result
}
