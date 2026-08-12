package live

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
)

// SubmitSignedOrder takes a validated SignedAction and the original SigningRequest,
// assembles the final signed payload, submits it to Pacifica via WS,
// and registers the order with the tracker for fill monitoring.
//
// This is the non-custodial submission path — the backend never touches the private key.
func (c *Client) SubmitSignedOrder(
	ctx context.Context,
	signed domain.SignedAction,
	req *domain.SigningRequest,
	tracker *Tracker,
) (*domain.SubmissionResult, error) {
	// Deserialize the unsigned payload back into the typed struct
	var unsigned PacificaUnsignedOrder
	if err := json.Unmarshal(req.UnsignedPayload, &unsigned); err != nil {
		return nil, fmt.Errorf("unmarshal unsigned payload: %w", err)
	}

	// Attach the user's signature to produce the final order
	finalOrder := AttachSignature(unsigned, signed, req)

	c.logger.Info("pacifica live: submitting signed order",
		"symbol", finalOrder.Symbol,
		"side", finalOrder.Side,
		"amount", finalOrder.Amount,
		"reduce_only", finalOrder.ReduceOnly,
		"client_order_id", finalOrder.ClientOrderID,
		"signer", signed.SignerAddress,
	)

	// Submit via the existing WS path
	sendOrder := c.sendOrder
	if c.sendSigned != nil {
		sendOrder = c.sendSigned
	}
	submitResult, err := sendOrder(ctx, finalOrder)
	if err != nil {
		if tracker != nil {
			tracker.Register(&SubmitResult{
				ClientOrderID: req.ClientOrderID,
				Symbol:        req.Symbol,
				SubmittedAt:   time.Now(),
			}, req.Amount)
		}
		return nil, fmt.Errorf("submit signed order: %w", err)
	}

	// Register with tracker for fill monitoring
	if submitResult.Accepted && tracker != nil {
		tracker.Register(submitResult, req.Amount)
	}

	logSubmitOutcome(c.logger, "pacifica", submitResult)

	return &domain.SubmissionResult{
		RequestID:     signed.RequestID,
		ClientOrderID: req.ClientOrderID,
		Venue:         "pacifica",
		OrderID:       submitResult.OrderID,
		Accepted:      submitResult.Accepted,
		Error:         submitResult.Error,
		SubmittedAt:   submitResult.SubmittedAt,
		RespondedAt:   submitResult.RespondedAt,
	}, nil
}

func (c *Client) SubmitSignedLeverage(
	ctx context.Context,
	signed domain.SignedAction,
	req *domain.SigningRequest,
) (*domain.SubmissionResult, error) {
	var unsigned PacificaUnsignedLeverage
	if err := json.Unmarshal(req.UnsignedPayload, &unsigned); err != nil {
		return nil, fmt.Errorf("unmarshal leverage payload: %w", err)
	}
	final := UpdateLeverageRequest{
		Account: req.Account, AgentWallet: req.Signer, Signature: signed.Signature,
		Timestamp: unsigned.Timestamp, ExpiryWindow: unsigned.ExpiryWindow,
		Symbol: unsigned.Symbol, Leverage: unsigned.Leverage,
	}
	send := c.sendLeverageUpdate
	if c.sendLeverage != nil {
		send = c.sendLeverage
	}
	result, err := send(ctx, final)
	if err != nil {
		return nil, fmt.Errorf("submit leverage update: %w", err)
	}
	return &domain.SubmissionResult{
		RequestID: signed.RequestID, ClientOrderID: req.ClientOrderID, Venue: "pacifica",
		Accepted: result.Accepted, Error: result.Error,
		SubmittedAt: result.SubmittedAt, RespondedAt: result.RespondedAt,
	}, nil
}

func logSubmitOutcome(logger *slog.Logger, venue string, result *SubmitResult) {
	if result.Accepted {
		logger.Info(venue+" live: signed order accepted",
			"order_id", result.OrderID,
			"client_order_id", result.ClientOrderID,
		)
	} else {
		logger.Warn(venue+" live: signed order rejected",
			"client_order_id", result.ClientOrderID,
			"error", result.Error,
		)
	}
}
