package orders

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Wire fields/routes follow rise-public c0b1fb879bb1fdb0ce43c6191aba48b5306cd2ac:
// rust/types/src/order_history_v2.rs and ts/src/api/{orders,trades}.
// History is scoped to the SDK-derived cross trader PDA, never a market-wide feed.
type History struct {
	base    string
	http    *http.Client
	mu      sync.Mutex
	retryAt time.Time
}

func NewHistory(base string, client *http.Client) *History {
	if base == "" {
		base = "https://perp-api.phoenix.trade"
	}
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	return &History{base: strings.TrimRight(base, "/"), http: client}
}

type page struct {
	Data       []json.RawMessage `json:"data"`
	HasMore    *bool             `json:"hasMore"`
	NextCursor *string           `json:"nextCursor"`
}

func (h *History) pages(ctx context.Context, path string, query url.Values, visit func(json.RawMessage) error, covered func() bool) error {
	seen := map[string]bool{}
	for range 4 {
		h.mu.Lock()
		blocked := time.Now().Before(h.retryAt)
		h.mu.Unlock()
		if blocked {
			return errors.New("Phoenix history reads backing off")
		}
		readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		req, err := http.NewRequestWithContext(readCtx, http.MethodGet, h.base+path+"?"+query.Encode(), nil)
		if err != nil {
			cancel()
			return errors.New("invalid Phoenix history endpoint")
		}
		res, err := h.http.Do(req)
		if err != nil {
			cancel()
			return errors.New("Phoenix history read unavailable")
		}
		if res.StatusCode == http.StatusTooManyRequests {
			delay := 5 * time.Second
			if seconds, err := strconv.ParseUint(res.Header.Get("Retry-After"), 10, 32); err == nil {
				delay = max(delay, time.Duration(seconds)*time.Second)
			} else if at, err := http.ParseTime(res.Header.Get("Retry-After")); err == nil {
				delay = max(delay, time.Until(at))
			}
			h.mu.Lock()
			h.retryAt = time.Now().Add(delay)
			h.mu.Unlock()
		}
		raw, readErr := io.ReadAll(io.LimitReader(res.Body, (4<<20)+1))
		_ = res.Body.Close()
		cancel()
		if res.StatusCode != http.StatusOK || readErr != nil || len(raw) > 4<<20 {
			return errors.New("Phoenix history response unavailable or oversized")
		}
		var p page
		if err := json.Unmarshal(raw, &p); err != nil || p.Data == nil || p.HasMore == nil || len(p.Data) > 100 {
			return errors.New("invalid Phoenix history page")
		}
		for _, row := range p.Data {
			if err := visit(row); err != nil {
				return err
			}
		}
		if covered != nil && covered() {
			return nil
		}
		if !*p.HasMore {
			return nil
		}
		if p.NextCursor == nil || *p.NextCursor == "" || len(*p.NextCursor) > 1024 || seen[*p.NextCursor] {
			return errors.New("invalid Phoenix history cursor")
		}
		seen[*p.NextCursor] = true
		query.Set("cursor", *p.NextCursor)
	}
	return errors.New("Phoenix history coverage exceeds bounded pagination")
}

type orderRow struct {
	PDA             *json.Number `json:"traderPdaIndex"`
	Subaccount      *json.Number `json:"traderSubaccountIndex"`
	Slot            *json.Number `json:"slot"`
	SlotIndex       *json.Number `json:"slotIndex"`
	Instruction     *json.Number `json:"instructionIndex"`
	Event           *json.Number `json:"eventIndex"`
	Symbol          string       `json:"marketSymbol"`
	InstructionType string       `json:"instructionType"`
	OrderType       string       `json:"orderType"`
	CreatedAt       time.Time    `json:"createdAt"`
	ParentOrderType *string      `json:"parentOrderType"`
	Status          string       `json:"status"`
	ReduceOnly      *bool        `json:"isReduceOnly"`
	StopLoss        *bool        `json:"isStopLoss"`
	Intended        struct {
		Kind           string       `json:"orderKind"`
		Side           string       `json:"side"`
		CID            *string      `json:"clientOrderId"`
		Ticks          *json.Number `json:"priceInTicks"`
		Lots           *json.Number `json:"baseLots"`
		LastSlot       *json.Number `json:"lastValidSlot"`
		Flags          *json.Number `json:"orderFlags"`
		BaseQuantity   string       `json:"baseQty"`
		HasLimit       *bool        `json:"hasPriceLimit"`
		ReduceOnly     *bool        `json:"isReduceOnly"`
		StopLoss       *bool        `json:"isStopLoss"`
		PostOnly       *bool        `json:"isPostOnly"`
		TakeOnly       *bool        `json:"isTakeOnly"`
		CancelExisting *bool        `json:"cancelExisting"`
	} `json:"intended"`
	Aggregate *struct {
		Base      string       `json:"totalBaseQtyFilled"`
		Quote     string       `json:"totalQuoteQtyFilled"`
		Fee       string       `json:"totalTakerFee"`
		NumTrades *json.Number `json:"numTrades"`
		NumFills  *json.Number `json:"totalNumFills"`
	} `json:"fillBeforePlacement"`
	Placed  json.RawMessage `json:"placedOrder"`
	Current json.RawMessage `json:"currentOrderState"`
}
type tradeRow struct {
	PDA             *json.Number `json:"traderPdaIndex"`
	Subaccount      *json.Number `json:"subaccountIndex"`
	Slot            *json.Number `json:"slot"`
	SlotIndex       *json.Number `json:"slotIndex"`
	Instruction     *json.Number `json:"instructionIndex"`
	Event           *json.Number `json:"eventIndex"`
	Symbol          string       `json:"marketSymbol"`
	Signature       *string      `json:"signature"`
	FillID          *string      `json:"fillId"`
	InstructionType string       `json:"instructionType"`
	TradeType       string       `json:"tradeType"`
	Liquidity       string       `json:"liquidity"`
	Before          string       `json:"baseLotsBefore"`
	After           string       `json:"baseLotsAfter"`
	Delta           string       `json:"baseLotsDelta"`
	QuoteBefore     string       `json:"virtualQuoteLotsBefore"`
	QuoteAfter      string       `json:"virtualQuoteLotsAfter"`
	QuoteDelta      string       `json:"virtualQuoteLotsDelta"`
	Price           string       `json:"price"`
}

func number(p *json.Number) (uint64, error) {
	if p == nil {
		return 0, ErrUncertain
	}
	return strconv.ParseUint(p.String(), 10, 64)
}
func isNumber(p *json.Number, want uint64) bool { v, err := number(p); return err == nil && v == want }
func isBool(p *bool, want bool) bool            { return p != nil && *p == want }
func null(raw json.RawMessage) bool {
	return len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

func decimal(s string) (*big.Rat, error) {
	if s == "" || len(s) > 96 || strings.ContainsAny(s, "/eE+ \t\n") {
		return nil, ErrUncertain
	}
	r, ok := new(big.Rat).SetString(s)
	if !ok || r.Sign() < 0 {
		return nil, ErrUncertain
	}
	return r, nil
}
func signedDelta(before, after, delta string) (*big.Int, error) {
	values := make([]*big.Int, 0, 3)
	for _, text := range []string{before, after, delta} {
		v, err := strconv.ParseInt(text, 10, 64)
		if err != nil || strconv.FormatInt(v, 10) != text {
			return nil, ErrUncertain
		}
		values = append(values, big.NewInt(v))
	}
	if new(big.Int).Sub(values[1], values[0]).Cmp(values[2]) != 0 {
		return nil, ErrUncertain
	}
	return values[2], nil
}

func (h *History) Resolve(ctx context.Context, a Attempt, slot uint64) (*Fill, error) {
	p, err := Decode(&a.Request)
	if err != nil {
		return nil, err
	}
	if slot < p.Metadata.MinContextSlot || slot > p.LastSlot {
		return nil, ErrUncertain
	}
	q := a.Request
	path := "/v1/traders/" + url.PathEscape(p.Metadata.Built.TraderAccount)
	query := url.Values{"market_symbol": {q.Symbol}, "limit": {"100"}, "organize_twap_orders": {"false"},
		"start_time": {q.CreatedAt.Add(-time.Minute).UTC().Format(time.RFC3339Nano)}, "end_time": {q.CreatedAt.Add(2 * time.Minute).UTC().Format(time.RFC3339Nano)}}
	var order *orderRow
	err = h.pages(ctx, path+"/orders_v2", query, func(raw json.RawMessage) error {
		var row orderRow
		if err := json.Unmarshal(raw, &row); err != nil {
			return err
		}
		if row.Intended.CID == nil || *row.Intended.CID != q.ClientOrderID {
			return nil
		}
		if order != nil {
			old, _ := json.Marshal(order)
			next, _ := json.Marshal(row)
			if !bytes.Equal(old, next) {
				return ErrUncertain
			}
			return nil
		}
		order = &row
		return nil
	}, nil)
	if err != nil {
		return nil, err
	}
	if order == nil {
		return nil, ErrUncertain
	}
	i := order.Intended
	flags := uint64(0)
	if q.ReduceOnly {
		flags = 128
	}
	wantBase, err := decimal(p.Metadata.Normalized.Amount)
	if err != nil {
		return nil, err
	}
	intendedBase, err := decimal(i.BaseQuantity)
	if err != nil {
		return nil, err
	}
	if !isNumber(order.PDA, 0) || !isNumber(order.Subaccount, 0) || !isNumber(order.Slot, slot) || order.Symbol != q.Symbol || order.ParentOrderType != nil ||
		order.InstructionType != "PlaceMarketOrder" || order.OrderType != "market" || i.Kind != "ImmediateOrCancel" || i.Side != q.Side ||
		!isNumber(i.Ticks, p.Ticks) || !isNumber(i.Lots, p.Lots) || !isNumber(i.LastSlot, p.LastSlot) || !isNumber(i.Flags, flags) || intendedBase.Cmp(wantBase) != 0 ||
		!isBool(i.HasLimit, true) || !isBool(i.ReduceOnly, q.ReduceOnly) || !isBool(order.ReduceOnly, q.ReduceOnly) || !isBool(i.StopLoss, false) || !isBool(order.StopLoss, false) ||
		!isBool(i.PostOnly, false) || !isBool(i.TakeOnly, true) || !isBool(i.CancelExisting, false) || !null(order.Placed) || !null(order.Current) ||
		order.CreatedAt.Before(q.CreatedAt.Add(-time.Minute)) || order.CreatedAt.After(q.CreatedAt.Add(2*time.Minute)) {
		return nil, ErrUncertain
	}
	switch order.Status {
	case "filled", "partially_filled", "partial_fill", "cancelled", "expired", "rejected":
	default:
		return nil, ErrUncertain
	}
	orderSlotIndex, e1 := number(order.SlotIndex)
	orderInstruction, e2 := number(order.Instruction)
	_, e3 := number(order.Event)
	if e1 != nil || e2 != nil || e3 != nil {
		return nil, ErrUncertain
	}
	if order.Aggregate == nil {
		return nil, ErrUncertain
	}
	expectedBase, err := decimal(order.Aggregate.Base)
	if err != nil {
		return nil, err
	}
	expectedTrades, err := number(order.Aggregate.NumTrades)
	if err != nil {
		return nil, err
	}
	totalLots := new(big.Int)
	unit := new(big.Rat).Quo(wantBase, new(big.Rat).SetInt(new(big.Int).SetUint64(p.Lots)))
	limit, err := decimal(p.Metadata.Normalized.LimitPrice)
	if err != nil {
		return nil, err
	}
	seen := map[string][]byte{}
	ids := []string{}
	err = h.pages(ctx, path+"/trades_v2", url.Values{"market_symbol": {q.Symbol}, "limit": {"100"}}, func(raw json.RawMessage) error {
		var row tradeRow
		if err := json.Unmarshal(raw, &row); err != nil {
			return err
		}
		if row.Signature == nil || *row.Signature != a.Signature {
			return nil
		}
		if row.Symbol != q.Symbol || !isNumber(row.PDA, 0) || !isNumber(row.Subaccount, 0) || !isNumber(row.Slot, slot) || !isNumber(row.SlotIndex, orderSlotIndex) || !isNumber(row.Instruction, orderInstruction) || row.InstructionType != "PlaceMarketOrder" || row.TradeType != "market" || row.Liquidity != "taker" {
			return ErrUncertain
		}
		event, err := number(row.Event)
		if err != nil {
			return err
		}
		id := fmt.Sprintf("%d:%d:%d:%d", slot, orderSlotIndex, orderInstruction, event)
		identity := id
		if row.FillID != nil {
			if *row.FillID == "" || len(*row.FillID) > 128 {
				return ErrUncertain
			}
			identity = "fill:" + *row.FillID
			id = *row.FillID + ":" + id
		}
		canonical, _ := json.Marshal(row)
		if previous, ok := seen[identity]; ok {
			if !bytes.Equal(previous, canonical) {
				return ErrUncertain
			}
			return nil
		}
		seen[identity] = canonical
		ids = append(ids, id)
		delta, err := signedDelta(row.Before, row.After, row.Delta)
		if err != nil || delta.Sign() == 0 || (q.Side == "buy") != (delta.Sign() > 0) {
			return ErrUncertain
		}
		if _, err := signedDelta(row.QuoteBefore, row.QuoteAfter, row.QuoteDelta); err != nil {
			return err
		}
		delta.Abs(delta)
		totalLots.Add(totalLots, delta)
		if totalLots.Cmp(new(big.Int).SetUint64(p.Lots)) > 0 {
			return ErrUncertain
		}
		price, err := decimal(row.Price)
		if err != nil || price.Sign() == 0 || (q.Side == "buy" && price.Cmp(limit) > 0) || (q.Side == "sell" && price.Cmp(limit) < 0) {
			return ErrUncertain
		}
		return nil
	}, func() bool {
		// trades_v2 has no submission-time filter. Once the terminal packet's
		// complete trade count and exact base quantity are covered, older wallet
		// lifetime history cannot improve this order's correlation proof.
		return uint64(len(ids)) == expectedTrades && new(big.Rat).Mul(unit, new(big.Rat).SetInt(totalLots)).Cmp(expectedBase) == 0
	})
	if err != nil {
		return nil, err
	}
	totalBase := new(big.Rat).Mul(unit, new(big.Rat).SetInt(totalLots))
	aggregateBase, aggregateQuote, fee := new(big.Rat), new(big.Rat), new(big.Rat)
	if order.Aggregate != nil {
		aggregateBase, err = decimal(order.Aggregate.Base)
		if err != nil {
			return nil, err
		}
		aggregateQuote, err = decimal(order.Aggregate.Quote)
		if err != nil {
			return nil, err
		}
		fee, err = decimal(order.Aggregate.Fee)
		if err != nil {
			return nil, err
		}
	}
	// Null/missing aggregate or empty history is indexer lag, never proof of
	// zero execution. A zero result needs explicit terminal zero-fill evidence.
	if order.Aggregate == nil || !isNumber(order.Aggregate.NumTrades, uint64(len(ids))) {
		return nil, ErrUncertain
	}
	numFills, err := number(order.Aggregate.NumFills)
	if err != nil || numFills < uint64(len(ids)) {
		return nil, ErrUncertain
	}
	if totalLots.Sign() == 0 && (numFills != 0 || (order.Status != "cancelled" && order.Status != "expired" && order.Status != "rejected")) {
		return nil, ErrUncertain
	}
	if aggregateBase.Cmp(totalBase) != 0 || (totalLots.Sign() == 0 && (fee.Sign() != 0 || aggregateQuote.Sign() != 0)) || (totalLots.Sign() > 0 && aggregateQuote.Sign() == 0) {
		return nil, ErrUncertain
	}
	// History prices may be averaged, while virtualQuoteLotsDelta is position
	// accounting (including closing basis), not executed quote notional. Use the
	// terminal order's exact aggregated quote quantity and verify its signed bound.
	if totalLots.Sign() > 0 {
		average := new(big.Rat).Quo(aggregateQuote, totalBase)
		if (q.Side == "buy" && average.Cmp(limit) > 0) || (q.Side == "sell" && average.Cmp(limit) < 0) {
			return nil, ErrUncertain
		}
	}
	status := "filled"
	if totalLots.Sign() == 0 {
		status = "no_fill"
	} else if totalLots.Uint64() < p.Lots {
		status = "partial_fill"
	}
	return &Fill{BaseLots: totalLots.String(), BaseQuantity: totalBase.FloatString(28), QuoteQuantity: order.Aggregate.Quote, ExchangeFee: order.Aggregate.Fee, Slot: slot, FillIDs: ids, Status: status}, nil
}
