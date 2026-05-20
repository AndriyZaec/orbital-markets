package live

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
)

// SubmitSignedOrder takes a validated SignedAction and the original SigningRequest,
// assembles the final signed payload, POSTs it to Hyperliquid,
// and registers the order with the tracker for fill monitoring.
//
// This is the non-custodial submission path — the backend never touches the private key.
func (c *Client) SubmitSignedOrder(
	ctx context.Context,
	signed domain.SignedAction,
	req *domain.SigningRequest,
) (*domain.SubmissionResult, error) {
	// Deserialize the unsigned payload back into the typed struct
	var unsigned HyperliquidUnsignedAction
	if err := json.Unmarshal(req.UnsignedPayload, &unsigned); err != nil {
		return nil, fmt.Errorf("unmarshal unsigned action: %w", err)
	}

	// Deserialize venue metadata to get the cloid for tracker registration
	var meta HyperliquidSubmitMeta
	if err := json.Unmarshal(req.VenueMetadata, &meta); err != nil {
		return nil, fmt.Errorf("unmarshal venue metadata: %w", err)
	}

	// Attach the user's signature to produce the final request body
	bodyBytes, err := AttachSignature(signed, unsigned)
	if err != nil {
		return nil, fmt.Errorf("attach signature: %w", err)
	}

	c.logger.Info("hyperliquid live: submitting signed order",
		"symbol", req.Symbol,
		"side", req.Side,
		"amount", req.Amount,
		"reduce_only", req.ReduceOnly,
		"client_order_id", req.ClientOrderID,
		"cloid", meta.Cloid,
		"signer", signed.SignerAddress,
	)

	submittedAt := time.Now()

	// POST to exchange
	httpReq, err := http.NewRequestWithContext(
		ctx, http.MethodPost, exchangeURL, bytes.NewReader(bodyBytes),
	)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		c.logger.Error("hyperliquid live: signed submit failed",
			"symbol", req.Symbol,
			"err", err,
		)
		return &domain.SubmissionResult{
			RequestID:     signed.RequestID,
			ClientOrderID: req.ClientOrderID,
			Venue:         "hyperliquid",
			Accepted:      false,
			Error:         err.Error(),
			SubmittedAt:   submittedAt,
			RespondedAt:   time.Now(),
		}, nil
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	respondedAt := time.Now()

	// Parse using the existing response parser
	submitResult := c.parseResponse(respBody, req.Symbol, meta.Cloid, submittedAt, respondedAt)

	// Register with tracker for fill monitoring
	if submitResult.Accepted && c.tracker != nil {
		c.tracker.Register(submitResult, req.Amount)
	}

	if submitResult.Accepted {
		c.logger.Info("hyperliquid live: signed order accepted",
			"order_id", submitResult.OrderID,
			"client_order_id", req.ClientOrderID,
			"cloid", meta.Cloid,
		)
	} else {
		c.logger.Warn("hyperliquid live: signed order rejected",
			"client_order_id", req.ClientOrderID,
			"cloid", meta.Cloid,
			"error", submitResult.Error,
		)
	}

	return &domain.SubmissionResult{
		RequestID:     signed.RequestID,
		ClientOrderID: req.ClientOrderID,
		Venue:         "hyperliquid",
		OrderID:       submitResult.OrderID,
		Accepted:      submitResult.Accepted,
		Error:         submitResult.Error,
		SubmittedAt:   submitResult.SubmittedAt,
		RespondedAt:   submitResult.RespondedAt,
	}, nil
}
