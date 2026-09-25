package dataagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
	asteraccount "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/aster/account"
)

type Reader interface {
	ReadAccount(context.Context, string) (AccountObservation, error)
	ReadLeverageBrackets(context.Context, string, string) (asteraccount.LeverageBrackets, time.Time, error)
	ReadFunding(context.Context, string, time.Time, time.Time) ([]venue.FundingPayment, error)
	LookupOrder(context.Context, string, string, string) (OrderStatus, error)
}

var _ Reader = (*Service)(nil)

type AccountObservation = asteraccount.Observation

type OrderStatus struct {
	OrderID          string
	ClientOrderID    string
	Symbol           string
	Status           string
	ExecutedQuantity float64
	AveragePrice     float64
	Fee              float64
}

type dataReader interface {
	readAccount(context.Context, string, string, []byte) (AccountObservation, error)
	readLeverageBrackets(context.Context, string, string, []byte, string) (asteraccount.LeverageBrackets, time.Time, error)
	readFunding(context.Context, string, string, []byte, time.Time, time.Time) ([]venue.FundingPayment, error)
	lookupOrder(context.Context, string, string, []byte, string, string) (OrderStatus, error)
}

func (s *Service) ReadLeverageBrackets(ctx context.Context, owner, symbol string) (asteraccount.LeverageBrackets, time.Time, error) {
	if !readSymbolPattern.MatchString(symbol) {
		return nil, time.Time{}, ErrInvalidInput
	}
	record, err := s.loadReadRecord(ctx, owner)
	if err != nil {
		return nil, time.Time{}, err
	}
	defer clear(record.PrivateKey)
	if s.reader == nil {
		return nil, time.Time{}, ErrUnavailable
	}
	return s.reader.readLeverageBrackets(ctx, record.Owner, record.AgentAddress, record.PrivateKey, symbol)
}

func (s *Service) ReadAccount(ctx context.Context, owner string) (AccountObservation, error) {
	record, err := s.loadReadRecord(ctx, owner)
	if err != nil {
		return AccountObservation{}, err
	}
	defer clear(record.PrivateKey)
	if s.reader == nil {
		return AccountObservation{}, ErrUnavailable
	}
	return s.reader.readAccount(ctx, record.Owner, record.AgentAddress, record.PrivateKey)
}

func (s *Service) LookupOrder(ctx context.Context, owner, symbol, clientOrderID string) (OrderStatus, error) {
	if !readSymbolPattern.MatchString(symbol) || !readClientIDPattern.MatchString(clientOrderID) {
		return OrderStatus{}, ErrInvalidInput
	}
	record, err := s.loadReadRecord(ctx, owner)
	if err != nil {
		return OrderStatus{}, err
	}
	defer clear(record.PrivateKey)
	if s.reader == nil {
		return OrderStatus{}, ErrUnavailable
	}
	return s.reader.lookupOrder(ctx, record.Owner, record.AgentAddress, record.PrivateKey, symbol, clientOrderID)
}

func (s *Service) ReadFunding(ctx context.Context, owner string, since, until time.Time) ([]venue.FundingPayment, error) {
	if since.IsZero() || until.IsZero() {
		return nil, ErrInvalidInput
	}
	since = time.UnixMilli(since.UnixMilli()).UTC()
	until = time.UnixMilli(until.UnixMilli()).UTC()
	if !since.Before(until) || until.After(s.now().Add(time.Minute)) {
		return nil, ErrInvalidInput
	}
	record, err := s.loadReadRecord(ctx, owner)
	if err != nil {
		return nil, err
	}
	defer clear(record.PrivateKey)
	if s.reader == nil {
		return nil, ErrUnavailable
	}
	return s.reader.readFunding(ctx, record.Owner, record.AgentAddress, record.PrivateKey, since, until)
}

func (s *Service) loadReadRecord(ctx context.Context, owner string) (Record, error) {
	if !addressPattern.MatchString(owner) {
		return Record{}, ErrInvalidInput
	}
	record, err := s.store.LoadApprovedByOwner(ctx, strings.ToLower(owner))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			status, statusErr := s.store.StatusByOwner(ctx, strings.ToLower(owner))
			if errors.Is(statusErr, ErrNotFound) {
				return Record{}, ErrNotApproved
			}
			if statusErr != nil {
				return Record{}, ErrUnavailable
			}
			return Record{}, stateError(status.Status)
		}
		return Record{}, publicLoadError(err)
	}
	if record.RequestedExpiry <= s.now().UnixMilli() {
		clear(record.PrivateKey)
		return Record{}, ErrNotApproved
	}
	return record, nil
}

func (c *Client) readAccount(ctx context.Context, owner, agent string, privateKey []byte) (AccountObservation, error) {
	observedAt := c.now().UTC()
	read := func(path string, params []pair) ([]byte, error) {
		body, err := c.signedGET(ctx, path, params, owner, agent, privateKey, c.nextNonce())
		if err != nil {
			if errors.Is(err, ErrReadRejected) {
				return nil, ErrReadRejected
			}
			return nil, fmt.Errorf("Aster %s read failed", endpointName(path))
		}
		return body, nil
	}
	modeBody, err := read("/fapi/v3/positionSide/dual", nil)
	if err != nil {
		return AccountObservation{}, err
	}
	mode, err := asteraccount.ParseSnapshotPart("get_position_mode", modeBody)
	if err != nil {
		return AccountObservation{}, fmt.Errorf("Aster position-mode response was invalid")
	}
	accountBody, err := read("/fapi/v3/accountWithJoinMargin", nil)
	if err != nil {
		return AccountObservation{}, err
	}
	margin, err := asteraccount.ParseSnapshotPart("get_account", accountBody)
	if err != nil {
		return AccountObservation{}, fmt.Errorf("Aster account response was invalid")
	}
	positionsBody, err := read("/fapi/v3/positionRisk", nil)
	if err != nil {
		return AccountObservation{}, err
	}
	positions, err := asteraccount.ParsePositions(positionsBody)
	if err != nil {
		return AccountObservation{}, fmt.Errorf("Aster position-risk response was invalid")
	}
	bracketsBody, err := read("/fapi/v3/leverageBracket", nil)
	if err != nil {
		return AccountObservation{}, err
	}
	brackets, err := asteraccount.ParseLeverageBrackets(bracketsBody, "")
	if err != nil {
		return AccountObservation{}, fmt.Errorf("Aster leverage-bracket response was invalid")
	}
	for _, position := range positions {
		if len(brackets[position.Symbol]) == 0 {
			return AccountObservation{}, fmt.Errorf("Aster leverage-bracket response was incomplete")
		}
	}
	return AccountObservation{
		DataAgent: agent, Margin: *margin.Margin, Positions: positions, PositionMode: *mode.Mode,
		LeverageBrackets: brackets, ObservedAt: observedAt,
	}, nil
}

func (c *Client) readLeverageBrackets(
	ctx context.Context,
	owner, agent string,
	privateKey []byte,
	symbol string,
) (asteraccount.LeverageBrackets, time.Time, error) {
	observedAt := c.now().UTC()
	body, err := c.signedGET(ctx, "/fapi/v3/leverageBracket", []pair{{"symbol", symbol}}, owner, agent, privateKey, c.nextNonce())
	if err != nil {
		if errors.Is(err, ErrReadRejected) {
			return nil, time.Time{}, ErrReadRejected
		}
		return nil, time.Time{}, fmt.Errorf("Aster leverage-bracket read failed")
	}
	brackets, err := asteraccount.ParseLeverageBrackets(body, symbol)
	if err != nil || len(brackets[symbol]) == 0 {
		return nil, time.Time{}, fmt.Errorf("Aster leverage-bracket response was invalid")
	}
	return brackets, observedAt, nil
}

func (c *Client) lookupOrder(
	ctx context.Context,
	owner, agent string,
	privateKey []byte,
	symbol, clientOrderID string,
) (OrderStatus, error) {
	body, err := c.signedGET(ctx, "/fapi/v3/order", []pair{
		{"symbol", symbol}, {"origClientOrderId", clientOrderID},
	}, owner, agent, privateKey, c.nextNonce())
	if err != nil {
		if ctx.Err() != nil {
			return OrderStatus{}, ctx.Err()
		}
		if errors.Is(err, ErrReadRejected) {
			return OrderStatus{}, ErrReadRejected
		}
		return OrderStatus{}, fmt.Errorf("Aster exact-order read failed")
	}
	order, err := parseOrder(body, symbol, clientOrderID)
	if err != nil {
		return OrderStatus{}, fmt.Errorf("Aster exact-order response was invalid")
	}
	if order.ExecutedQuantity > 0 {
		tradesBody, readErr := c.signedGET(ctx, "/fapi/v3/userTrades", []pair{
			{"symbol", symbol}, {"orderId", order.OrderID}, {"limit", "1000"},
		}, owner, agent, privateKey, c.nextNonce())
		if readErr == nil {
			if fee, feeErr := parseOrderFee(tradesBody, symbol, order.OrderID, order.ExecutedQuantity); feeErr == nil {
				order.Fee = fee
			}
		}
	}
	return order, nil
}

func parseOrderFee(body []byte, symbol, orderID string, executedQuantity float64) (float64, error) {
	var trades []struct {
		OrderID         json.RawMessage `json:"orderId"`
		Symbol          string          `json:"symbol"`
		Quantity        string          `json:"qty"`
		Commission      string          `json:"commission"`
		CommissionAsset string          `json:"commissionAsset"`
	}
	if err := json.Unmarshal(body, &trades); err != nil || len(trades) == 0 {
		return 0, fmt.Errorf("invalid trades")
	}
	var quantity, fee float64
	for _, trade := range trades {
		tradeOrderID := strings.Trim(string(trade.OrderID), `"`)
		tradeQuantity, quantityErr := strconv.ParseFloat(trade.Quantity, 64)
		commission, commissionErr := strconv.ParseFloat(trade.Commission, 64)
		if tradeOrderID != orderID || trade.Symbol != symbol || trade.CommissionAsset != "USDT" ||
			quantityErr != nil || commissionErr != nil || tradeQuantity <= 0 ||
			math.IsNaN(tradeQuantity) || math.IsInf(tradeQuantity, 0) ||
			math.IsNaN(commission) || math.IsInf(commission, 0) {
			return 0, fmt.Errorf("invalid trade")
		}
		quantity += tradeQuantity
		fee += math.Abs(commission)
	}
	if math.Abs(quantity-executedQuantity) > math.Max(1e-12, executedQuantity*1e-9) {
		return 0, fmt.Errorf("incomplete trades")
	}
	return fee, nil
}

func (c *Client) readFunding(
	ctx context.Context,
	owner, agent string,
	privateKey []byte,
	since, until time.Time,
) ([]venue.FundingPayment, error) {
	const (
		requestLimit = 100
		resultLimit  = 1000
	)
	payments := make([]venue.FundingPayment, 0)
	seen := make(map[string]bool)
	requests := 0
	var readRange func(time.Time, time.Time) error
	readRange = func(start, end time.Time) error {
		requests++
		if requests > requestLimit {
			return fmt.Errorf("Aster funding request limit exceeded")
		}
		body, err := c.signedGET(ctx, "/fapi/v3/income", []pair{
			{"incomeType", "FUNDING_FEE"}, {"startTime", strconv.FormatInt(start.UnixMilli(), 10)},
			{"endTime", strconv.FormatInt(end.UnixMilli(), 10)}, {"limit", strconv.Itoa(resultLimit)},
		}, owner, agent, privateKey, c.nextNonce())
		if err != nil {
			return fmt.Errorf("Aster funding-income read failed")
		}
		rangePayments, err := asteraccount.ParseFundingPayments(body, owner, c.now())
		if err != nil || len(rangePayments) > resultLimit {
			return fmt.Errorf("Aster funding-income response was invalid")
		}
		for index, payment := range rangePayments {
			if payment.PaidAt.Before(start) || payment.PaidAt.After(end) ||
				index > 0 && payment.PaidAt.Before(rangePayments[index-1].PaidAt) {
				return fmt.Errorf("Aster funding-income response was invalid")
			}
		}
		if len(rangePayments) == resultLimit {
			if start.Equal(end) {
				return fmt.Errorf("Aster funding-income range remained saturated")
			}
			mid := time.UnixMilli(start.UnixMilli() + (end.UnixMilli()-start.UnixMilli())/2).UTC()
			if err := readRange(start, mid); err != nil {
				return err
			}
			return readRange(mid.Add(time.Millisecond), end)
		}
		for _, payment := range rangePayments {
			if seen[payment.ExternalID] {
				return fmt.Errorf("Aster funding-income response was invalid")
			}
			seen[payment.ExternalID] = true
			payments = append(payments, payment)
		}
		return nil
	}
	for start := since.UTC(); !start.After(until); {
		end := start.Add(incomeWindow)
		if end.After(until) {
			end = until.UTC()
		}
		if err := readRange(start, end); err != nil {
			return nil, err
		}
		start = end.Add(time.Millisecond)
	}
	sort.Slice(payments, func(i, j int) bool {
		if payments[i].PaidAt.Equal(payments[j].PaidAt) {
			return payments[i].ExternalID < payments[j].ExternalID
		}
		return payments[i].PaidAt.Before(payments[j].PaidAt)
	})
	return payments, nil
}
