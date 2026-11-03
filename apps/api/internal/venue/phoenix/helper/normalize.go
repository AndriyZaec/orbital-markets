package helper

import (
	"context"
	"errors"
	"math/big"
)

type NormalizeRequest struct {
	Symbol             string `json:"symbol"`
	Side               Side   `json:"side"`
	Amount             string `json:"amount"`
	LimitPrice         string `json:"limit_price"`
	BaseLotDecimals    int8   `json:"base_lot_decimals"`
	TickSize           string `json:"tick_size"`
	MetadataGeneration uint64 `json:"metadata_generation"`
}

type NormalizedOrder struct {
	Symbol             string `json:"symbol"`
	Side               Side   `json:"side"`
	Amount             string `json:"amount"`
	LimitPrice         string `json:"limit_price"`
	NumBaseLots        uint64 `json:"num_base_lots"`
	PriceLimitTicks    uint64 `json:"price_limit_ticks"`
	BaseLotDecimals    int8   `json:"base_lot_decimals"`
	TickSize           string `json:"tick_size"`
	MetadataGeneration uint64 `json:"metadata_generation"`
}

func (c *Client) NormalizeOrder(ctx context.Context, q NormalizeRequest) (NormalizedOrder, error) {
	var r NormalizedOrder
	if err := c.call(ctx, "normalize_order", q, &r); err != nil {
		return NormalizedOrder{}, err
	}
	if err := ValidateNormalizedOrder(q, r); err != nil {
		c.stop()
		return NormalizedOrder{}, err
	}
	return r, nil
}

// Check the returned boundary without reimplementing SDK lot/tick conversion.
func ValidateNormalizedOrder(q NormalizeRequest, r NormalizedOrder) error {
	if r.Symbol != q.Symbol || r.Side != q.Side || r.BaseLotDecimals != q.BaseLotDecimals || r.TickSize != q.TickSize || r.MetadataGeneration != q.MetadataGeneration || r.NumBaseLots == 0 || r.PriceLimitTicks == 0 || r.NumBaseLots > 1<<32-1 || r.PriceLimitTicks > 1<<32-1 {
		return errors.New("Phoenix normalization context mismatch")
	}
	amount, ok := new(big.Rat).SetString(q.Amount)
	if !ok || amount.Sign() <= 0 {
		return errors.New("invalid approved amount")
	}
	normalized, ok := new(big.Rat).SetString(r.Amount)
	if !ok || normalized.Sign() <= 0 || normalized.Cmp(amount) > 0 {
		return errors.New("Phoenix normalization widened amount")
	}
	limit, ok := new(big.Rat).SetString(q.LimitPrice)
	if !ok || limit.Sign() <= 0 {
		return errors.New("invalid approved limit")
	}
	price, ok := new(big.Rat).SetString(r.LimitPrice)
	if !ok || price.Sign() <= 0 || (q.Side == SideBid && price.Cmp(limit) > 0) || (q.Side == SideAsk && price.Cmp(limit) < 0) || (q.Side != SideBid && q.Side != SideAsk) {
		return errors.New("Phoenix normalization widened price")
	}
	return nil
}
