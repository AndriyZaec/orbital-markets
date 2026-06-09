package live

import (
	"context"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
)

// VenueClientAdapter wraps the Pacifica live Client behind the shared VenueClient interface.
type VenueClientAdapter struct {
	client  *Client
	tracker *Tracker
}

func NewVenueClient(client *Client, tracker *Tracker) *VenueClientAdapter {
	return &VenueClientAdapter{client: client, tracker: tracker}
}

func (a *VenueClientAdapter) Name() string {
	return "pacifica"
}

func (a *VenueClientAdapter) SubmitOpen(ctx context.Context, params venue.OpenParams) (*venue.SubmitResult, error) {
	result, err := a.client.SubmitMarketOrder(
		ctx,
		params.Symbol,
		params.Side,
		params.Amount,
		params.Leverage,
		params.MarginRequired,
		params.ClientOrderID,
	)
	if err != nil {
		return nil, err
	}
	return convertSubmitResult("pacifica", result), nil
}

func (a *VenueClientAdapter) SubmitClose(ctx context.Context, params venue.CloseParams) (*venue.SubmitResult, error) {
	result, err := a.client.SubmitCloseOrder(
		ctx,
		params.Symbol,
		params.PositionSide,
		params.Amount,
		params.ClientOrderID,
	)
	if err != nil {
		return nil, err
	}
	return convertSubmitResult("pacifica", result), nil
}

func (a *VenueClientAdapter) WaitForFill(ctx context.Context, clientOrderID string) (*venue.FillResult, error) {
	result, err := a.tracker.WaitForFill(ctx, clientOrderID)
	if err != nil {
		return nil, err
	}
	return convertFillResult("pacifica", result), nil
}

func convertSubmitResult(venueName string, r *SubmitResult) *venue.SubmitResult {
	return &venue.SubmitResult{
		Venue:         venueName,
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

func convertFillResult(venueName string, r *FillResult) *venue.FillResult {
	return &venue.FillResult{
		Venue:           venueName,
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
