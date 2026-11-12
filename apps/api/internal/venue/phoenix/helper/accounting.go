package helper

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
)

type AccountingRequest struct {
	OwnerAuthority        string          `json:"owner_authority"`
	BuilderAuthority      *string         `json:"builder_authority"`
	ExpectedMessageBase64 string          `json:"expected_message_base64"`
	TransactionBase64     string          `json:"transaction_base64"`
	InnerInstructions     json.RawMessage `json:"inner_instructions"`
}
type FundingSettlement struct {
	EventIndex       uint32 `json:"event_index"`
	Symbol           string `json:"symbol"`
	AssetID          uint32 `json:"asset_id"`
	PaymentQuoteLots string `json:"payment_quote_lots"`
}
type AccountingResult struct {
	OwnerAuthority       string              `json:"owner_authority"`
	TraderAccount        string              `json:"trader_account"`
	FlightFeeQuoteLots   *string             `json:"flight_fee_quote_lots"`
	FlightTransferEvents uint32              `json:"flight_transfer_events"`
	DecodedEventCount    uint32              `json:"decoded_event_count"`
	FundingSettlements   []FundingSettlement `json:"funding_settlements"`
}

func (c *Client) DecodeAccounting(ctx context.Context, q AccountingRequest) (AccountingResult, error) {
	var r AccountingResult
	if err := c.call(ctx, "decode_accounting", q, &r); err != nil {
		return r, err
	}
	if r.OwnerAuthority != q.OwnerAuthority || r.DecodedEventCount == 0 || r.DecodedEventCount > 1024 || r.FlightTransferEvents > r.DecodedEventCount || len(r.FundingSettlements) > int(r.DecodedEventCount) {
		c.stop()
		return AccountingResult{}, errors.New("Phoenix accounting scope/count mismatch")
	}
	if r.FlightFeeQuoteLots != nil {
		v, err := strconv.ParseUint(*r.FlightFeeQuoteLots, 10, 64)
		if err != nil || strconv.FormatUint(v, 10) != *r.FlightFeeQuoteLots {
			c.stop()
			return AccountingResult{}, errors.New("invalid observed Flight fee")
		}
	}
	if q.BuilderAuthority == nil && (r.FlightFeeQuoteLots == nil || *r.FlightFeeQuoteLots != "0" || r.FlightTransferEvents != 0) {
		c.stop()
		return AccountingResult{}, errors.New("native accounting invented a Flight fee")
	}
	for _, event := range r.FundingSettlements {
		if event.EventIndex >= r.DecodedEventCount || event.Symbol == "" {
			c.stop()
			return AccountingResult{}, errors.New("invalid funding settlement event")
		}
		v, err := strconv.ParseInt(event.PaymentQuoteLots, 10, 64)
		if err != nil || strconv.FormatInt(v, 10) != event.PaymentQuoteLots {
			c.stop()
			return AccountingResult{}, errors.New("invalid observed funding payment")
		}
	}
	return r, nil
}
