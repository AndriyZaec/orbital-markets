package live

import (
	"context"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
)

// VenueClientAdapter wraps the Hyperliquid live Client behind the shared VenueClient interface.
type VenueClientAdapter struct {
	client *Client
}

func NewVenueClient(client *Client) *VenueClientAdapter {
	return &VenueClientAdapter{client: client}
}

func (a *VenueClientAdapter) Name() string {
	return "hyperliquid"
}

func (a *VenueClientAdapter) SubmitOpen(ctx context.Context, params venue.OpenParams) (*venue.SubmitResult, error) {
	result, err := a.client.SubmitMarketOrder(
		ctx,
		params.Symbol,
		params.Side,
		params.Amount,
		params.Price,
		params.Leverage,
		params.MarginRequired,
		params.ClientOrderID,
	)
	if err != nil {
		return nil, err
	}
	return convertSubmitResult(result), nil
}

func (a *VenueClientAdapter) SubmitClose(ctx context.Context, params venue.CloseParams) (*venue.SubmitResult, error) {
	result, err := a.client.SubmitCloseOrder(
		ctx,
		params.Symbol,
		params.PositionSide,
		params.Amount,
		params.Price,
		params.ClientOrderID,
	)
	if err != nil {
		return nil, err
	}
	return convertSubmitResult(result), nil
}

func (a *VenueClientAdapter) WaitForFill(ctx context.Context, clientOrderID string) (*venue.FillResult, error) {
	result, err := a.client.WaitForFill(ctx, clientOrderID)
	if err != nil {
		return nil, err
	}
	return convertFillResult(result), nil
}

func convertSubmitResult(r *SubmitResult) *venue.SubmitResult {
	return &venue.SubmitResult{
		Venue:         "hyperliquid",
		RequestID:     r.RequestID,
		OrderID:       r.OrderID,
		ClientOrderID: r.ClientOrderID,
		Symbol:        r.Symbol,
		Accepted:      r.Accepted,
		Error:         r.Error,
		SubmittedAt:   r.SubmittedAt,
		RespondedAt:   r.RespondedAt,
	}
}

func convertFillResult(r *FillResult) *venue.FillResult {
	return &venue.FillResult{
		Venue:           "hyperliquid",
		OrderID:         r.OrderID,
		ClientOrderID:   r.ClientOrderID,
		Symbol:          r.Symbol,
		Status:          venue.OrderStatus(r.Status),
		RequestedAmount: r.RequestedAmount,
		FilledAmount:    r.FilledAmount,
		AvgFillPrice:    r.AvgFillPrice,
		FillCount:       r.FillCount,
		TotalFee:        r.TotalFee,
		SubmittedAt:     r.SubmittedAt,
		FirstFillAt:     r.FirstFillAt,
		LastFillAt:      r.LastFillAt,
		ResolvedAt:      r.ResolvedAt,
		Error:           r.Error,
	}
}
