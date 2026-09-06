package account

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
)

type PositionMode struct {
	OneWay bool
}

type MarginSummary struct {
	CanTrade  bool
	Equity    float64
	Available float64
}

type Position struct {
	Symbol           string
	Side             string
	Size             float64
	EntryPrice       float64
	UnrealizedPnL    float64
	Leverage         float64
	LiquidationPrice float64
	MarginUsed       float64
}

type SnapshotPart struct {
	Mode      *PositionMode
	Margin    *MarginSummary
	Positions *[]Position
}

type LeverageUpdate struct {
	Symbol   string
	Leverage float64
}

type LeverageBracket struct {
	InitialLeverage float64
	NotionalFloor   float64
	NotionalCap     float64
}

type LeverageBrackets map[string][]LeverageBracket

type Update struct {
	SnapshotPart     *SnapshotPart
	Leverage         *LeverageUpdate
	LeverageBrackets LeverageBrackets
}

func ParseSnapshotPart(operation string, body []byte) (SnapshotPart, error) {
	switch operation {
	case "get_position_mode":
		var response struct {
			DualSidePosition *bool `json:"dualSidePosition"`
		}
		if err := decodeJSON(body, &response); err != nil || response.DualSidePosition == nil {
			return SnapshotPart{}, fmt.Errorf("invalid position mode")
		}
		return SnapshotPart{Mode: &PositionMode{OneWay: !*response.DualSidePosition}}, nil
	case "get_account":
		var response struct {
			CanTrade           *bool           `json:"canTrade"`
			TotalMarginBalance string          `json:"totalMarginBalance"`
			AvailableBalance   string          `json:"availableBalance"`
			Positions          json.RawMessage `json:"positions"`
		}
		if err := decodeJSON(body, &response); err != nil || response.CanTrade == nil || !jsonArray(response.Positions) {
			return SnapshotPart{}, fmt.Errorf("invalid account snapshot")
		}
		equity, err := finiteDecimal(response.TotalMarginBalance)
		if err != nil || equity < 0 {
			return SnapshotPart{}, fmt.Errorf("invalid account equity")
		}
		available, err := finiteDecimal(response.AvailableBalance)
		if err != nil || available < 0 {
			return SnapshotPart{}, fmt.Errorf("invalid available balance")
		}
		return SnapshotPart{Margin: &MarginSummary{
			CanTrade: *response.CanTrade, Equity: equity, Available: available,
		}}, nil
	case "get_positions":
		positions, err := ParsePositions(body)
		if err != nil {
			return SnapshotPart{}, err
		}
		return SnapshotPart{Positions: &positions}, nil
	default:
		return SnapshotPart{}, fmt.Errorf("unsupported Aster snapshot operation: %s", operation)
	}
}

func ParsePositions(body []byte) ([]Position, error) {
	var response []struct {
		Symbol           string `json:"symbol"`
		PositionSide     string `json:"positionSide"`
		PositionAmount   string `json:"positionAmt"`
		EntryPrice       string `json:"entryPrice"`
		UnrealizedPnL    string `json:"unRealizedProfit"`
		Leverage         string `json:"leverage"`
		LiquidationPrice string `json:"liquidationPrice"`
		IsolatedMargin   string `json:"isolatedMargin"`
	}
	if err := decodeJSON(body, &response); err != nil || !jsonArray(body) {
		return nil, fmt.Errorf("invalid positions")
	}
	positions := make([]Position, 0, len(response))
	seen := make(map[string]struct{}, len(response))
	for _, raw := range response {
		if !validSymbol(raw.Symbol) || (raw.PositionSide != "BOTH" && raw.PositionSide != "LONG" && raw.PositionSide != "SHORT") {
			return nil, fmt.Errorf("invalid position identity")
		}
		key := raw.Symbol + ":" + raw.PositionSide
		if _, duplicate := seen[key]; duplicate {
			return nil, fmt.Errorf("duplicate position %s", key)
		}
		seen[key] = struct{}{}
		amount, err := finiteDecimal(raw.PositionAmount)
		if err != nil {
			return nil, fmt.Errorf("invalid %s position amount", raw.Symbol)
		}
		entry, err := nonNegativeDecimal(raw.EntryPrice)
		if err != nil {
			return nil, fmt.Errorf("invalid %s entry price", raw.Symbol)
		}
		liquidation, err := nonNegativeDecimal(raw.LiquidationPrice)
		if err != nil {
			return nil, fmt.Errorf("invalid %s liquidation price", raw.Symbol)
		}
		leverage, err := nonNegativeDecimal(raw.Leverage)
		if err != nil || leverage > 125 {
			return nil, fmt.Errorf("invalid %s leverage", raw.Symbol)
		}
		unrealized := 0.0
		if raw.UnrealizedPnL != "" {
			unrealized, err = finiteDecimal(raw.UnrealizedPnL)
			if err != nil {
				return nil, fmt.Errorf("invalid %s unrealized PnL", raw.Symbol)
			}
		}
		margin := 0.0
		if raw.IsolatedMargin != "" {
			margin, err = nonNegativeDecimal(raw.IsolatedMargin)
			if err != nil {
				return nil, fmt.Errorf("invalid %s margin", raw.Symbol)
			}
		}
		side := ""
		size := math.Abs(amount)
		switch raw.PositionSide {
		case "LONG":
			if amount < 0 {
				return nil, fmt.Errorf("negative LONG position for %s", raw.Symbol)
			}
			side = "long"
		case "SHORT":
			if amount > 0 {
				return nil, fmt.Errorf("positive SHORT position for %s", raw.Symbol)
			}
			side = "short"
		case "BOTH":
			if amount > 0 {
				side = "long"
			} else if amount < 0 {
				side = "short"
			}
		}
		if size == 0 {
			continue
		}
		positions = append(positions, Position{
			Symbol: raw.Symbol, Side: side, Size: size, EntryPrice: entry,
			UnrealizedPnL: unrealized, Leverage: leverage,
			LiquidationPrice: liquidation, MarginUsed: margin,
		})
	}
	return positions, nil
}

func ParseLeverageUpdate(body []byte) (LeverageUpdate, error) {
	var response struct {
		Symbol   string `json:"symbol"`
		Leverage int    `json:"leverage"`
	}
	if err := decodeJSON(body, &response); err != nil || !validSymbol(response.Symbol) || response.Leverage < 1 || response.Leverage > 125 {
		return LeverageUpdate{}, fmt.Errorf("invalid leverage update")
	}
	return LeverageUpdate{Symbol: response.Symbol, Leverage: float64(response.Leverage)}, nil
}

func ParseLeverageBrackets(body []byte, expectedSymbol string) (LeverageBrackets, error) {
	type response struct {
		Symbol   string `json:"symbol"`
		Brackets []struct {
			InitialLeverage int     `json:"initialLeverage"`
			NotionalCap     float64 `json:"notionalCap"`
			NotionalFloor   float64 `json:"notionalFloor"`
		} `json:"brackets"`
	}
	parse := func(raw response) ([]LeverageBracket, error) {
		if !validSymbol(raw.Symbol) || (expectedSymbol != "" && raw.Symbol != expectedSymbol) || len(raw.Brackets) == 0 {
			return nil, fmt.Errorf("invalid leverage brackets")
		}
		brackets := make([]LeverageBracket, 0, len(raw.Brackets))
		for _, bracket := range raw.Brackets {
			if bracket.InitialLeverage < 1 || bracket.InitialLeverage > 125 ||
				!finiteNumber(bracket.NotionalCap) || bracket.NotionalCap <= 0 ||
				!finiteNumber(bracket.NotionalFloor) || bracket.NotionalFloor < 0 || bracket.NotionalFloor >= bracket.NotionalCap {
				return nil, fmt.Errorf("invalid %s leverage bracket", raw.Symbol)
			}
			brackets = append(brackets, LeverageBracket{
				InitialLeverage: float64(bracket.InitialLeverage),
				NotionalFloor:   bracket.NotionalFloor, NotionalCap: bracket.NotionalCap,
			})
		}
		sort.Slice(brackets, func(i, j int) bool { return brackets[i].NotionalFloor < brackets[j].NotionalFloor })
		for i := 1; i < len(brackets); i++ {
			if brackets[i].NotionalFloor < brackets[i-1].NotionalCap {
				return nil, fmt.Errorf("overlapping %s leverage brackets", raw.Symbol)
			}
		}
		return brackets, nil
	}
	result := make(LeverageBrackets)
	if expectedSymbol != "" {
		var raw response
		if err := decodeJSON(body, &raw); err != nil {
			return nil, fmt.Errorf("invalid leverage brackets")
		}
		brackets, err := parse(raw)
		if err != nil {
			return nil, err
		}
		result[raw.Symbol] = brackets
		return result, nil
	}
	var responses []response
	if err := decodeJSON(body, &responses); err != nil || !jsonArray(body) || len(responses) == 0 {
		return nil, fmt.Errorf("invalid leverage brackets")
	}
	for _, raw := range responses {
		if _, duplicate := result[raw.Symbol]; duplicate {
			return nil, fmt.Errorf("duplicate leverage brackets for %s", raw.Symbol)
		}
		brackets, err := parse(raw)
		if err != nil {
			return nil, err
		}
		result[raw.Symbol] = brackets
	}
	return result, nil
}

func decodeJSON(body []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("unexpected trailing JSON")
		}
		return err
	}
	return nil
}

func finiteDecimal(value string) (float64, error) {
	if value == "" || strings.TrimSpace(value) != value {
		return 0, fmt.Errorf("invalid decimal")
	}
	number, err := strconv.ParseFloat(value, 64)
	if err != nil || !finiteNumber(number) {
		return 0, fmt.Errorf("invalid decimal")
	}
	return number, nil
}

func nonNegativeDecimal(value string) (float64, error) {
	number, err := finiteDecimal(value)
	if err != nil || number < 0 {
		return 0, fmt.Errorf("invalid non-negative decimal")
	}
	return number, nil
}

func finiteNumber(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func jsonArray(value []byte) bool {
	trimmed := bytes.TrimSpace(value)
	return len(trimmed) > 0 && trimmed[0] == '['
}

func validSymbol(symbol string) bool {
	if len(symbol) == 0 || len(symbol) > 32 {
		return false
	}
	for _, char := range symbol {
		if (char < 'A' || char > 'Z') && (char < '0' || char > '9') && char != '_' {
			return false
		}
	}
	return true
}
