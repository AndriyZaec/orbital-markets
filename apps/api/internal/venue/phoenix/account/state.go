// Package account maintains read-only Phoenix cross-trader state. Wire rows
// follow phoenix-rise-types 0.6.13; this package does not compute Phoenix risk.
package account

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mr-tron/base58"
)

const Freshness = 30 * time.Second

var ErrSequenceGap = errors.New("Phoenix trader-state sequence gap")

type CapabilityAccess struct {
	Immediate         bool `json:"immediate"`
	ViaColdActivation bool `json:"viaColdActivation"`
}

type Capabilities struct {
	Flags   uint16 `json:"flags"`
	State   string `json:"state"`
	Actions struct {
		PlaceLimitOrder     CapabilityAccess `json:"placeLimitOrder"`
		PlaceMarketOrder    CapabilityAccess `json:"placeMarketOrder"`
		RiskIncreasingTrade CapabilityAccess `json:"riskIncreasingTrade"`
		RiskReducingTrade   CapabilityAccess `json:"riskReducingTrade"`
		DepositCollateral   CapabilityAccess `json:"depositCollateral"`
		WithdrawCollateral  CapabilityAccess `json:"withdrawCollateral"`
	} `json:"capabilities"`
}

// Position and Order preserve integer lots and decimal strings exactly. Base
// quantities and funding cannot be valued without current exchange metadata.
type Position struct {
	Symbol                      string          `json:"symbol"`
	PositionSequenceNumber      string          `json:"positionSequenceNumber"`
	BasePositionLots            string          `json:"basePositionLots"`
	EntryPriceTicks             string          `json:"entryPriceTicks"`
	EntryPriceUSD               string          `json:"entryPriceUsd"`
	VirtualQuotePositionLots    string          `json:"virtualQuotePositionLots"`
	UnsettledFundingQuoteLots   string          `json:"unsettledFundingQuoteLots"`
	AccumulatedFundingQuoteLots string          `json:"accumulatedFundingQuoteLots"`
	TakeProfitTriggers          json.RawMessage `json:"takeProfitTriggers,omitempty"`
	StopLossTriggers            json.RawMessage `json:"stopLossTriggers,omitempty"`
	ConditionalTakeProfit       json.RawMessage `json:"conditionalTakeProfitTriggers,omitempty"`
	ConditionalStopLoss         json.RawMessage `json:"conditionalStopLossTriggers,omitempty"`
}

type Order struct {
	Change              string `json:"change,omitempty"`
	OrderSequenceNumber string `json:"orderSequenceNumber"`
	Side                string `json:"side"`
	OrderType           string `json:"orderType"`
	PriceTicks          string `json:"priceTicks"`
	PriceUSD            string `json:"priceUsd"`
	SizeRemainingLots   string `json:"sizeRemainingLots"`
	InitialSizeLots     string `json:"initialSizeLots"`
	ReduceOnly          bool   `json:"reduceOnly"`
	Status              string `json:"status"`
	IsStopLoss          bool   `json:"isStopLoss"`
	ConditionalKind     string `json:"conditionalKind,omitempty"`
	ScaleSetID          *uint8 `json:"scaleSetId,omitempty"`
}

type SpotCollateral struct {
	AssetIndex uint32 `json:"assetIndex"`
	Symbol     string `json:"symbol"`
	Balance    string `json:"balance"`
	Decimals   uint8  `json:"decimals"`
	MaxBalance string `json:"maxBalance,omitempty"`
}

type CrossState struct {
	Collateral                 string
	SpotCollaterals            []SpotCollateral
	Capabilities               Capabilities
	MakerFeeOverrideMultiplier float64
	TakerFeeOverrideMultiplier float64
	Positions                  map[string]Position
	Orders                     map[string]map[string]Order
	Splines                    map[string]json.RawMessage
	Triggers                   map[string]json.RawMessage
	CooldownStatus             json.RawMessage
}

type Snapshot struct {
	Owner              string
	Connected          bool
	Synchronized       bool
	Generation         uint64
	Slot               uint64
	Sequence           uint64
	ObservedAt         time.Time
	PositionsUpdatedAt time.Time
	UnavailableReason  string
	Cross              CrossState
}

func (s Snapshot) Fresh(now time.Time) bool {
	return s.Connected && s.Synchronized && !s.ObservedAt.IsZero() &&
		!now.Before(s.ObservedAt) && now.Sub(s.ObservedAt) <= Freshness
}

// State belongs to one owner. Connection generations fence in-flight REST reads
// and discard stream continuity after a disconnect. Old data remains diagnostic.
type State struct {
	mu                 sync.RWMutex
	snapshot           Snapshot
	haveSnapshot       bool
	haveStreamSnapshot bool
}

func NewState(owner string) (*State, error) {
	if !ValidOwner(owner) {
		return nil, errors.New("invalid Phoenix owner authority")
	}
	return &State{snapshot: Snapshot{Owner: owner, UnavailableReason: "Phoenix account snapshot required"}}, nil
}

func ValidOwner(owner string) bool {
	if len(owner) < 32 || len(owner) > 44 {
		return false
	}
	key, err := base58.Decode(owner)
	return err == nil && len(key) == 32
}

func (s *State) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := s.snapshot
	result.Cross = cloneCross(result.Cross)
	return result
}

func (s *State) begin() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshot.Generation++
	s.snapshot.Connected = false
	s.invalidateLocked("Phoenix account stream snapshot required")
	s.haveStreamSnapshot = false
	return s.snapshot.Generation
}

func (s *State) connected(generation uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.snapshot.Generation == generation {
		s.snapshot.Connected = true
	}
}

func (s *State) disconnect(generation uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.snapshot.Generation == generation {
		s.snapshot.Connected = false
		s.haveStreamSnapshot = false
		s.invalidateLocked("Phoenix account stream disconnected; snapshot required")
	}
}

func (s *State) invalidateLocked(reason string) {
	s.snapshot.Synchronized = false
	s.snapshot.PositionsUpdatedAt = time.Time{}
	s.snapshot.UnavailableReason = reason
}

type message struct {
	Channel                    string        `json:"channel"`
	Authority                  string        `json:"authority"`
	TraderPDAIndex             *uint8        `json:"traderPdaIndex"`
	Slot                       *uint64       `json:"slot"`
	MessageType                string        `json:"messageType"`
	Version                    *uint32       `json:"version"`
	Capabilities               *Capabilities `json:"capabilities"`
	MakerFeeOverrideMultiplier *float64      `json:"makerFeeOverrideMultiplier"`
	TakerFeeOverrideMultiplier *float64      `json:"takerFeeOverrideMultiplier"`
	Subaccounts                []subaccount  `json:"subaccounts"`
	Deltas                     []subaccount  `json:"deltas"`
}

type subaccount struct {
	Index           *uint8            `json:"subaccountIndex"`
	Sequence        *uint64           `json:"sequence"`
	Collateral      string            `json:"collateral"`
	SpotCollaterals []SpotCollateral  `json:"spotCollaterals"`
	Capabilities    *Capabilities     `json:"capabilities"`
	CooldownStatus  json.RawMessage   `json:"cooldownStatus"`
	Positions       []json.RawMessage `json:"positions"`
	Orders          []struct {
		Symbol string  `json:"symbol"`
		Orders []Order `json:"orders"`
	} `json:"orders"`
	Splines  []json.RawMessage `json:"splines"`
	Triggers []json.RawMessage `json:"triggers"`
}

func decodeREST(raw []byte) (message, error) {
	var response struct {
		Authority      string          `json:"authority"`
		TraderPDAIndex *uint8          `json:"traderPdaIndex"`
		Slot           *uint64         `json:"slot"`
		Snapshot       json.RawMessage `json:"snapshot"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return message{}, err
	}
	var m message
	if err := json.Unmarshal(response.Snapshot, &m); err != nil {
		return message{}, err
	}
	m.Authority, m.TraderPDAIndex, m.Slot = response.Authority, response.TraderPDAIndex, response.Slot
	m.MessageType = "snapshot"
	return m, nil
}

func (s *State) apply(generation uint64, m message, observedAt time.Time, stream bool) (err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.snapshot.Generation != generation {
		return nil
	}
	defer func() {
		if err != nil {
			s.invalidateLocked("Phoenix account state invalid; snapshot required")
		}
	}()
	if m.Authority != s.snapshot.Owner || m.TraderPDAIndex == nil || *m.TraderPDAIndex != 0 || m.Slot == nil || observedAt.IsZero() {
		return errors.New("Phoenix trader-state identity or slot mismatch")
	}
	if m.MessageType == "snapshot" {
		cross, sequence, err := snapshotCross(m)
		if err != nil {
			return err
		}
		if s.haveSnapshot && (*m.Slot < s.snapshot.Slot || sequence < s.snapshot.Sequence) {
			if stream {
				return errors.New("Phoenix stream snapshot is behind current account state")
			}
			return nil // A concurrent REST response must never roll state back or refresh its age.
		}
		if observedAt.Before(s.snapshot.ObservedAt) {
			return nil
		}
		if err := boundedCross(cross); err != nil {
			return err
		}
		s.snapshot.Cross, s.snapshot.Sequence = cross, sequence
		s.haveSnapshot = true
		if stream {
			s.haveStreamSnapshot = true
		}
	} else if m.MessageType == "delta" {
		if !stream || !s.haveStreamSnapshot || !s.snapshot.Synchronized {
			return ErrSequenceGap
		}
		// Validate and apply to a copy: a later bad row cannot partially mutate exposure.
		cross := cloneCross(s.snapshot.Cross)
		sequence := s.snapshot.Sequence
		changed := false
		for _, delta := range m.Deltas {
			if delta.Index == nil {
				return errors.New("Phoenix delta missing subaccount index")
			}
			if *delta.Index != 0 {
				continue
			}
			if delta.Sequence == nil {
				return errors.New("Phoenix delta missing sequence")
			}
			if *delta.Sequence <= sequence {
				continue
			}
			if sequence == math.MaxUint64 || *delta.Sequence != sequence+1 {
				return ErrSequenceGap
			}
			if *m.Slot < s.snapshot.Slot {
				return errors.New("Phoenix delta slot regressed")
			}
			if err := applySubaccount(&cross, delta, false); err != nil {
				return err
			}
			sequence, changed = *delta.Sequence, true
		}
		if !changed {
			return nil
		} // Duplicates, isolated updates and control traffic do not refresh cross state.
		if err := boundedCross(cross); err != nil {
			return err
		}
		s.snapshot.Cross, s.snapshot.Sequence = cross, sequence
	} else {
		return fmt.Errorf("unknown Phoenix trader-state message type %q", m.MessageType)
	}
	s.snapshot.Slot = *m.Slot
	if observedAt.After(s.snapshot.ObservedAt) {
		s.snapshot.ObservedAt = observedAt
	}
	s.snapshot.Synchronized = s.haveStreamSnapshot && s.snapshot.Connected
	if s.snapshot.Synchronized {
		s.snapshot.PositionsUpdatedAt = s.snapshot.ObservedAt
		s.snapshot.UnavailableReason = ""
	}
	return nil
}

func snapshotCross(m message) (CrossState, uint64, error) {
	if m.Version == nil || *m.Version != 1 || m.Capabilities == nil || m.Capabilities.State == "" ||
		m.MakerFeeOverrideMultiplier == nil || m.TakerFeeOverrideMultiplier == nil ||
		!nonnegativeFinite(*m.MakerFeeOverrideMultiplier) || !nonnegativeFinite(*m.TakerFeeOverrideMultiplier) {
		return CrossState{}, 0, errors.New("unsupported or incomplete Phoenix account snapshot")
	}
	cross := CrossState{Capabilities: *m.Capabilities,
		MakerFeeOverrideMultiplier: *m.MakerFeeOverrideMultiplier, TakerFeeOverrideMultiplier: *m.TakerFeeOverrideMultiplier,
		Positions: make(map[string]Position), Orders: make(map[string]map[string]Order),
		Splines: make(map[string]json.RawMessage), Triggers: make(map[string]json.RawMessage)}
	found := false
	var sequence uint64
	for _, sub := range m.Subaccounts {
		if sub.Index == nil {
			return CrossState{}, 0, errors.New("Phoenix snapshot missing subaccount index")
		}
		if *sub.Index != 0 {
			continue
		}
		if found || sub.Sequence == nil {
			return CrossState{}, 0, errors.New("duplicate or unsequenced Phoenix cross subaccount")
		}
		if err := applySubaccount(&cross, sub, true); err != nil {
			return CrossState{}, 0, err
		}
		sequence, found = *sub.Sequence, true
	}
	if !found {
		return CrossState{}, 0, errors.New("Phoenix cross subaccount does not exist")
	}
	return cross, sequence, nil
}

func applySubaccount(cross *CrossState, sub subaccount, snapshot bool) error {
	// Preserve the wire collateral string; do not infer equity or available margin.
	if !decimal(sub.Collateral, false) {
		return errors.New("invalid Phoenix collateral")
	}
	cross.Collateral = sub.Collateral
	seenSpot := make(map[uint32]bool)
	for _, spot := range sub.SpotCollaterals {
		if spot.Symbol == "" || !unsigned(spot.Balance) || (spot.MaxBalance != "" && !unsigned(spot.MaxBalance)) || seenSpot[spot.AssetIndex] {
			return errors.New("invalid Phoenix spot collateral")
		}
		seenSpot[spot.AssetIndex] = true
	}
	// Both snapshots and deltas carry the full spot set, including an empty set.
	cross.SpotCollaterals = sub.SpotCollaterals
	if sub.Capabilities != nil {
		if sub.Capabilities.State == "" {
			return errors.New("invalid Phoenix capabilities")
		}
		cross.Capabilities = *sub.Capabilities
	}
	if len(sub.CooldownStatus) != 0 && string(sub.CooldownStatus) != "null" {
		cross.CooldownStatus = sub.CooldownStatus
	}
	for _, raw := range sub.Positions {
		var position Position
		var change struct {
			Symbol   string          `json:"symbol"`
			Change   string          `json:"change"`
			Position json.RawMessage `json:"position"`
		}
		if snapshot {
			if err := json.Unmarshal(raw, &position); err != nil {
				return err
			}
		} else {
			if err := json.Unmarshal(raw, &change); err != nil {
				return err
			}
			if change.Symbol == "" {
				return errors.New("Phoenix position delta missing symbol")
			}
			if change.Change == "closed" {
				delete(cross.Positions, change.Symbol)
				continue
			}
			if change.Change != "updated" || len(change.Position) == 0 || string(change.Position) == "null" {
				return errors.New("invalid Phoenix position delta")
			}
			if err := json.Unmarshal(change.Position, &position); err != nil {
				return err
			}
			position.Symbol = change.Symbol
		}
		if err := validatePosition(position); err != nil {
			return err
		}
		if snapshot {
			if _, exists := cross.Positions[position.Symbol]; exists {
				return errors.New("duplicate Phoenix position")
			}
		}
		cross.Positions[position.Symbol] = position
	}
	for _, group := range sub.Orders {
		if group.Symbol == "" {
			return errors.New("Phoenix order group missing symbol")
		}
		if cross.Orders[group.Symbol] == nil {
			cross.Orders[group.Symbol] = make(map[string]Order)
		}
		for _, order := range group.Orders {
			if !unsigned(order.OrderSequenceNumber) {
				return errors.New("invalid Phoenix order sequence")
			}
			if !snapshot && order.Change == "closed" {
				delete(cross.Orders[group.Symbol], order.OrderSequenceNumber)
				continue
			}
			if order.Change != "" && order.Change != "updated" {
				return errors.New("invalid Phoenix order change")
			}
			if (order.Side != "bid" && order.Side != "ask") || order.OrderType == "" || order.Status == "" ||
				!unsigned(order.PriceTicks) || !decimal(order.PriceUSD, true) || !unsigned(order.SizeRemainingLots) || !unsigned(order.InitialSizeLots) {
				return errors.New("invalid Phoenix order")
			}
			remaining, _ := strconv.ParseUint(order.SizeRemainingLots, 10, 64)
			initial, _ := strconv.ParseUint(order.InitialSizeLots, 10, 64)
			if remaining > initial {
				return errors.New("Phoenix order remaining size exceeds initial size")
			}
			if snapshot {
				if _, exists := cross.Orders[group.Symbol][order.OrderSequenceNumber]; exists {
					return errors.New("duplicate Phoenix order")
				}
			}
			order.Change = ""
			cross.Orders[group.Symbol][order.OrderSequenceNumber] = order
		}
	}
	if err := applyResources(cross.Splines, sub.Splines, "spline", snapshot); err != nil {
		return err
	}
	return applyResources(cross.Triggers, sub.Triggers, "triggers", snapshot)
}

// Retain full spline regions and trigger rows for later SDK risk inputs rather
// than flattening them to counts or silently treating them as absent.
func applyResources(target map[string]json.RawMessage, rows []json.RawMessage, field string, snapshot bool) error {
	for _, raw := range rows {
		var envelope struct {
			Symbol string `json:"symbol"`
			Change string `json:"change"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			return err
		}
		if envelope.Symbol == "" {
			return errors.New("Phoenix resource missing symbol")
		}
		row := raw
		if !snapshot {
			if envelope.Change == "closed" {
				delete(target, envelope.Symbol)
				continue
			}
			if envelope.Change != "updated" {
				return errors.New("invalid Phoenix resource delta")
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(raw, &fields); err != nil {
				return err
			}
			row = fields[field]
			if len(row) == 0 || row[0] != '{' {
				return errors.New("Phoenix resource delta missing row")
			}
		} else if _, exists := target[envelope.Symbol]; exists {
			return errors.New("duplicate Phoenix resource")
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(row, &fields); err != nil {
			return err
		}
		if field == "spline" {
			for _, key := range []string{"midPriceTicks", "bidFilledAmountLots", "askFilledAmountLots"} {
				var value string
				if err := json.Unmarshal(fields[key], &value); err != nil || !unsigned(value) {
					return errors.New("invalid Phoenix spline row")
				}
			}
		} else {
			for _, key := range []string{"takeProfitTriggers", "stopLossTriggers", "conditionalTakeProfitTriggers", "conditionalStopLossTriggers"} {
				if value := fields[key]; len(value) != 0 && value[0] != '[' {
					return errors.New("invalid Phoenix trigger row")
				}
			}
		}
		target[envelope.Symbol] = row
	}
	return nil
}

func validatePosition(p Position) error {
	if p.Symbol == "" || !unsigned(p.PositionSequenceNumber) || !unsigned(p.EntryPriceTicks) || !decimal(p.EntryPriceUSD, true) {
		return errors.New("invalid Phoenix position")
	}
	for _, value := range []string{p.BasePositionLots, p.VirtualQuotePositionLots, p.UnsettledFundingQuoteLots, p.AccumulatedFundingQuoteLots} {
		if _, err := strconv.ParseInt(value, 10, 64); err != nil {
			return errors.New("invalid Phoenix signed position lots")
		}
	}
	return nil
}

func unsigned(value string) bool { _, err := strconv.ParseUint(value, 10, 64); return err == nil }
func nonnegativeFinite(value float64) bool {
	return value >= 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}
func decimal(value string, nonnegative bool) bool {
	// ParseFloat also accepts hexadecimal/exponent spellings that are not the
	// API's human-readable decimal format.
	digits := strings.TrimPrefix(value, "-")
	parts := strings.Split(digits, ".")
	if len(parts) > 2 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		for _, digit := range part {
			if digit < '0' || digit > '9' {
				return false
			}
		}
	}
	parsed, err := strconv.ParseFloat(value, 64)
	return err == nil && !math.IsNaN(parsed) && !math.IsInf(parsed, 0) && (!nonnegative || parsed >= 0)
}

func boundedCross(cross CrossState) error {
	raw, err := json.Marshal(cross)
	if err != nil || len(raw) > maxMessageBytes {
		return errors.New("Phoenix account state exceeds size limit")
	}
	return nil
}

func cloneCross(cross CrossState) CrossState {
	// These values originate only from JSON and contain no unsupported types.
	// A round trip also copies nested RawMessages, slices and optional pointers.
	raw, _ := json.Marshal(cross)
	var result CrossState
	_ = json.Unmarshal(raw, &result)
	return result
}
