package live

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
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
		c.registerAmbiguousSubmission(meta.Cloid, req, submittedAt)
		return nil, fmt.Errorf("submit signed order: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		c.registerAmbiguousSubmission(meta.Cloid, req, submittedAt)
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

func (c *Client) SubmitSignedLeverage(
	ctx context.Context,
	signed domain.SignedAction,
	req *domain.SigningRequest,
) (*domain.SubmissionResult, error) {
	var unsigned HyperliquidUnsignedLeverage
	if err := json.Unmarshal(req.UnsignedPayload, &unsigned); err != nil {
		return nil, fmt.Errorf("unmarshal leverage action: %w", err)
	}
	body, err := AttachLeverageSignature(signed, unsigned)
	if err != nil {
		return nil, fmt.Errorf("attach leverage signature: %w", err)
	}
	submittedAt := time.Now()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, exchangeURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build leverage request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("submit leverage update: %w", err)
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read leverage response: %w", err)
	}
	respondedAt := time.Now()
	var parsed struct {
		Status   string          `json:"status"`
		Response json.RawMessage `json:"response"`
	}
	if err := json.Unmarshal(responseBody, &parsed); err != nil {
		return nil, fmt.Errorf("parse leverage response: %w", err)
	}
	result := &domain.SubmissionResult{
		RequestID: signed.RequestID, ClientOrderID: req.ClientOrderID, Venue: "hyperliquid",
		Accepted: parsed.Status == "ok", SubmittedAt: submittedAt, RespondedAt: respondedAt,
	}
	if !result.Accepted {
		result.Error = strings.Trim(string(parsed.Response), `"`)
	}
	return result, nil
}

func (c *Client) registerAmbiguousSubmission(cloid string, req *domain.SigningRequest, submittedAt time.Time) {
	if c.tracker == nil {
		return
	}
	c.tracker.Register(&SubmitResult{
		ClientOrderID: cloid,
		Symbol:        req.Symbol,
		SubmittedAt:   submittedAt,
	}, req.Amount)
}
