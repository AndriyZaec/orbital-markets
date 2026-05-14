package executor

import "context"

// VenueLeg represents one side of a spread trade on a specific venue.
type VenueLeg struct {
	Venue          string
	Symbol         string
	Side           string  // "long" or "short"
	Amount         float64
	Price          float64 // expected price for slippage calculation
	Leverage       float64
	MarginRequired float64
}

// VenueClient is the interface each venue's live client must satisfy
// for the cross-venue executor. Keeps venue-specific logic behind the boundary.
type VenueClient interface {
	Name() string

	// SubmitOpen submits a market order to open a position leg.
	// Returns a structured result — accepted does NOT mean filled.
	SubmitOpen(ctx context.Context, leg VenueLeg, clientOrderID string) (*VenueSubmitResult, error)

	// SubmitClose submits a reduce-only market order to close/unwind a position leg.
	SubmitClose(ctx context.Context, leg VenueLeg, clientOrderID string) (*VenueSubmitResult, error)

	// WaitForFill waits for the order to reach a terminal fill state.
	WaitForFill(ctx context.Context, clientOrderID string) (*VenueFillResult, error)
}

// VenueSubmitResult is the venue-agnostic submit outcome.
type VenueSubmitResult struct {
	OrderID       string
	ClientOrderID string
	Accepted      bool
	Error         string
}

// VenueFillResult is the venue-agnostic fill outcome.
type VenueFillResult struct {
	OrderID       string
	ClientOrderID string
	Filled        bool   // fully filled
	Partial       bool   // partially filled
	FilledAmount  float64
	AvgFillPrice  float64
	Fee           float64
	Error         string
}
