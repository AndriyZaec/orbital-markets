package account

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

const browserHeartbeatTTL = 6 * time.Minute

type AccountStateSnapshot struct {
	Account                   string
	Agent                     string
	SnapshotID                string
	OneWayModeKnown           bool
	OneWayMode                bool
	CanTradeKnown             bool
	CanTrade                  bool
	Equity                    float64
	Available                 float64
	Positions                 []Position
	LeverageBySymbol          map[string]float64
	LeverageBrackets          LeverageBrackets
	LeverageBracketsUpdatedAt map[string]time.Time
	PositionsUpdatedAt        time.Time
	LastUpdated               time.Time
	Connected                 bool
	UnavailableReason         string
}

type AccountState struct {
	mu                        sync.RWMutex
	account                   string
	agent                     string
	snapshotID                string
	snapshotCreatedAt         time.Time
	mode                      *PositionMode
	margin                    *MarginSummary
	positions                 *[]Position
	modeUpdatedAt             time.Time
	marginUpdatedAt           time.Time
	positionsUpdatedAt        time.Time
	refreshID                 string
	refreshCreatedAt          time.Time
	refreshMargin             *MarginSummary
	refreshPositions          *[]Position
	refreshMarginUpdatedAt    time.Time
	refreshPositionsUpdatedAt time.Time
	leverageBySymbol          map[string]float64
	leverageBrackets          LeverageBrackets
	leverageBracketsUpdatedAt map[string]time.Time
	unavailableReason         string
}

func NewAccountState(account string) *AccountState {
	return &AccountState{
		account:                   strings.ToLower(strings.TrimSpace(account)),
		leverageBySymbol:          make(map[string]float64),
		leverageBrackets:          make(LeverageBrackets),
		leverageBracketsUpdatedAt: make(map[string]time.Time),
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
		if (!s.snapshotCreatedAt.IsZero() && !createdAt.After(s.snapshotCreatedAt)) ||
			(!s.refreshCreatedAt.IsZero() && !createdAt.After(s.refreshCreatedAt)) {
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
		s.refreshID = ""
		s.refreshCreatedAt = time.Time{}
		s.refreshMargin = nil
		s.refreshPositions = nil
		s.refreshMarginUpdatedAt = time.Time{}
		s.refreshPositionsUpdatedAt = time.Time{}
		s.unavailableReason = ""
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

func (s *AccountState) ApplyRefreshPart(
	account, agent, refreshID string,
	createdAt, respondedAt time.Time,
	part SnapshotPart,
) error {
	account = strings.ToLower(strings.TrimSpace(account))
	agent = strings.ToLower(strings.TrimSpace(agent))
	if account == "" || account != s.account || agent == "" || refreshID == "" || createdAt.IsZero() || respondedAt.Before(createdAt) {
		return fmt.Errorf("invalid Aster account refresh context")
	}
	if (part.Margin == nil) == (part.Positions == nil) || part.Mode != nil {
		return fmt.Errorf("Aster account refresh must contain margin or positions")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.mode == nil || agent != s.agent {
		return fmt.Errorf("full Aster account snapshot required")
	}
	if refreshID != s.refreshID {
		if (!s.snapshotCreatedAt.IsZero() && !createdAt.After(s.snapshotCreatedAt)) ||
			(!s.refreshCreatedAt.IsZero() && !createdAt.After(s.refreshCreatedAt)) {
			return nil
		}
		s.refreshID = refreshID
		s.refreshCreatedAt = createdAt
		s.refreshMargin = nil
		s.refreshPositions = nil
		s.refreshMarginUpdatedAt = time.Time{}
		s.refreshPositionsUpdatedAt = time.Time{}
	} else if !createdAt.Equal(s.refreshCreatedAt) {
		return fmt.Errorf("Aster account refresh generation mismatch")
	}
	if part.Margin != nil {
		margin := *part.Margin
		s.refreshMargin = &margin
		s.refreshMarginUpdatedAt = respondedAt
	}
	if part.Positions != nil {
		positions := append([]Position(nil), (*part.Positions)...)
		s.refreshPositions = &positions
		s.refreshPositionsUpdatedAt = respondedAt
	}
	if s.refreshMargin == nil || s.refreshPositions == nil {
		return nil
	}
	s.margin = s.refreshMargin
	s.positions = s.refreshPositions
	s.marginUpdatedAt = s.refreshMarginUpdatedAt
	s.positionsUpdatedAt = s.refreshPositionsUpdatedAt
	s.snapshotID = s.refreshID
	s.snapshotCreatedAt = s.refreshCreatedAt
	s.unavailableReason = ""
	for _, position := range *s.positions {
		s.leverageBySymbol[position.Symbol] = position.Leverage
	}
	s.refreshID = ""
	s.refreshMargin = nil
	s.refreshPositions = nil
	s.refreshMarginUpdatedAt = time.Time{}
	s.refreshPositionsUpdatedAt = time.Time{}
	return nil
}

func (s *AccountState) MarkUnavailable(account, agent, reason string) error {
	account = strings.ToLower(strings.TrimSpace(account))
	agent = strings.ToLower(strings.TrimSpace(agent))
	if account == "" || account != s.account || agent == "" || strings.TrimSpace(reason) == "" {
		return fmt.Errorf("invalid Aster account unavailable context")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.agent = agent
	s.snapshotID = ""
	s.snapshotCreatedAt = time.Time{}
	s.mode = nil
	s.margin = nil
	s.positions = nil
	s.modeUpdatedAt = time.Time{}
	s.marginUpdatedAt = time.Time{}
	s.positionsUpdatedAt = time.Time{}
	s.refreshID = ""
	s.refreshCreatedAt = time.Time{}
	s.refreshMargin = nil
	s.refreshPositions = nil
	s.refreshMarginUpdatedAt = time.Time{}
	s.refreshPositionsUpdatedAt = time.Time{}
	s.leverageBySymbol = make(map[string]float64)
	s.leverageBrackets = make(LeverageBrackets)
	s.leverageBracketsUpdatedAt = make(map[string]time.Time)
	s.unavailableReason = strings.TrimSpace(reason)
	return nil
}

func (s *AccountState) ApplyLeverage(update LeverageUpdate) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.leverageBySymbol[update.Symbol] = update.Leverage
}

func (s *AccountState) ApplyLeverageBrackets(brackets LeverageBrackets, updatedAt time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for symbol, tiers := range brackets {
		if !updatedAt.After(s.leverageBracketsUpdatedAt[symbol]) {
			continue
		}
		s.leverageBrackets[symbol] = append([]LeverageBracket(nil), tiers...)
		s.leverageBracketsUpdatedAt[symbol] = updatedAt
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
		LeverageBySymbol:          copyMap(s.leverageBySymbol),
		LeverageBrackets:          copyBrackets(s.leverageBrackets),
		LeverageBracketsUpdatedAt: copyTimeMap(s.leverageBracketsUpdatedAt),
		PositionsUpdatedAt:        s.positionsUpdatedAt,
		UnavailableReason:         s.unavailableReason,
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
		snapshot.LastUpdated = oldest(s.marginUpdatedAt, s.positionsUpdatedAt)
		newestUpdate := newest(s.marginUpdatedAt, s.positionsUpdatedAt)
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

func copyTimeMap(source map[string]time.Time) map[string]time.Time {
	result := make(map[string]time.Time, len(source))
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
