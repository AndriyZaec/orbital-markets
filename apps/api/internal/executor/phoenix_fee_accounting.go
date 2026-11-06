package executor

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strings"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/orders"
)

// SyncPhoenixObservedFees repairs a position's aggregate opening fee only after
// every constituent attempt is accounted. A merged hedge/retry fill must never
// be overwritten with only the last retry's fee. Repeated sync repairs a failed
// DB update without re-fetching transaction evidence or changing PnL semantics.
func (s *Store) SyncPhoenixObservedFees(ctx context.Context, position *LivePosition) error {
	owner := position.AccountBindings["phoenix"]
	if owner == "" {
		return nil
	}
	journal, err := s.PhoenixOrderJournal()
	if err != nil {
		return err
	}
	fills, err := s.GetFills(ctx, position.ID)
	if err != nil {
		return err
	}
	var requests struct {
		Leg1  *domain.SigningRequest `json:"leg_1_open_request"`
		Leg2  *domain.SigningRequest `json:"leg_2_open_request"`
		Retry *domain.SigningRequest `json:"leg_2_retry_request"`
	}
	record, recordErr := s.GetDurableSessionForPlan(ctx, position.PlanID)
	if recordErr == nil {
		if err := json.Unmarshal(record.Payload, &requests); err != nil {
			return err
		}
	} else if !errors.Is(recordErr, sql.ErrNoRows) {
		return recordErr
	}
	for _, fill := range fills {
		if fill.Venue != "phoenix" || !fill.Filled {
			continue
		}
		var group []*domain.SigningRequest
		if fill.Leg == 1 {
			group = []*domain.SigningRequest{requests.Leg1}
		} else if fill.Leg == 2 {
			group = []*domain.SigningRequest{requests.Leg2, requests.Retry}
		}
		if len(group) == 0 || group[0] == nil {
			a, err := journal.ByClientID(ctx, owner, fill.ClientOrderID)
			if errors.Is(err, orders.ErrNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			group = []*domain.SigningRequest{&a.Request}
		}
		quantity, total := new(big.Rat), new(big.Rat)
		complete, matched := true, false
		for _, request := range group {
			if request == nil {
				continue
			}
			if request.Venue != "phoenix" || request.Account != owner || request.Action != "open" || request.Symbol != fill.Symbol {
				return orders.ErrConflict
			}
			a, err := journal.Get(ctx, owner, request.ID)
			if errors.Is(err, orders.ErrNotFound) {
				complete = false
				break
			}
			if err != nil {
				return err
			}
			if a.Request.ClientOrderID != request.ClientOrderID {
				return orders.ErrConflict
			}
			if a.State == "failed" || a.State == "no_fill" {
				continue
			}
			if a.State != "filled" || a.Fill == nil {
				complete = false
				break
			}
			fee, known := a.Fill.TotalTradingFee()
			if !known || fee == nil {
				complete = false
				break
			}
			base, ok := new(big.Rat).SetString(a.Fill.BaseQuantity)
			if !ok || base.Sign() < 0 {
				return orders.ErrConflict
			}
			quantity.Add(quantity, base)
			total.Add(total, fee)
			if a.Signature == fill.OrderID && a.Request.ClientOrderID == fill.ClientOrderID {
				matched = true
			}
		}
		if !complete || !matched {
			continue
		}
		amount, _ := quantity.Float64()
		fee, _ := total.Float64()
		if math.IsInf(fee, 0) || math.IsNaN(fee) || math.Abs(amount-fill.FilledAmount) > math.Abs(fill.FilledAmount)*1e-12 {
			return orders.ErrConflict
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE live_fills SET fee=? WHERE position_id=? AND leg=? AND venue='phoenix' AND client_order_id=? AND order_id=?`, fee, position.ID, fill.Leg, fill.ClientOrderID, fill.OrderID); err != nil {
			return err
		}
	}
	return nil
}

// A shared cross account may contain pre-existing exposure in this very market.
// Symbol filtering alone cannot allocate its entire payment to one Orbital leg.
// Require matching payment size/side; otherwise coverage remains provisional.
func validatePhoenixFundingAttribution(ctx context.Context, tx *sql.Tx, positionID string, payment venue.FundingPayment) error {
	var amount float64
	var side, symbol string
	if err := tx.QueryRowContext(ctx, `SELECT filled_amount,side,symbol FROM live_fills WHERE position_id=? AND venue='phoenix' AND filled=1`, positionID).Scan(&amount, &side, &symbol); err != nil {
		return err
	}
	size, ok := new(big.Rat).SetString(payment.PositionSize)
	if !ok || size.Sign() <= 0 {
		return fmt.Errorf("Phoenix funding allocation requires position-size evidence")
	}
	observed, _ := size.Float64()
	if symbol != payment.MarketKey || strings.ToLower(side) != strings.ToLower(payment.PositionSide) || math.IsInf(observed, 0) || math.Abs(observed-amount) > math.Abs(amount)*1e-12 {
		return fmt.Errorf("Phoenix account-wide funding cannot be allocated to this strategy leg")
	}
	return nil
}
