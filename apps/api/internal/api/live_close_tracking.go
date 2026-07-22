package api

import (
	"context"
	"fmt"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/executor"
)

const (
	closeFillThreshold   = 0.995
	closeSubmissionGrace = 5 * time.Second
)

func (s *Server) trackCloseSubmission(req *domain.SigningRequest, result *domain.SubmissionResult) {
	if req.PositionID == "" || req.Leg == 0 {
		return
	}
	outcome := executor.CloseOutcome{
		PositionID: req.PositionID, Leg: req.Leg, Venue: req.Venue,
		ClientOrderID: req.ClientOrderID, OrderID: result.OrderID,
		RequestedAmount: req.Amount, Accepted: result.Accepted,
		Resolved: !result.Accepted, Error: result.Error,
	}
	if err := s.liveStore.UpsertCloseOutcome(s.ctx, outcome); err != nil {
		s.liveStore.MarkCloseDegraded(s.ctx, req.PositionID)
		return
	}
	if !result.Accepted {
		s.markCloseFailed(s.ctx, req, result.Error)
		return
	}
	if err := s.liveStore.MarkClosing(s.ctx, req.PositionID); err != nil {
		s.liveStore.MarkCloseDegraded(s.ctx, req.PositionID)
		return
	}
	s.liveStore.InsertEvent(s.ctx, req.PositionID, "close_leg_submitted", executor.ExecStateClosing,
		fmt.Sprintf("leg=%d venue=%s client_order_id=%s", req.Leg, req.Venue, req.ClientOrderID))

	go s.awaitCloseFill(req, result.OrderID)
}

func (s *Server) recordCloseSubmissionFailure(ctx context.Context, req *domain.SigningRequest, reason string) {
	if req == nil || req.PositionID == "" || req.Leg == 0 {
		return
	}
	_ = s.liveStore.UpsertCloseOutcome(ctx, executor.CloseOutcome{
		PositionID: req.PositionID, Leg: req.Leg, Venue: req.Venue,
		ClientOrderID: req.ClientOrderID, RequestedAmount: req.Amount,
		Resolved: true, Error: reason,
	})
	s.markCloseFailed(ctx, req, reason)
}

func (s *Server) awaitCloseFill(req *domain.SigningRequest, orderID string) {
	ctx, cancel := context.WithTimeout(s.ctx, 20*time.Second)
	defer cancel()

	fill, err := s.waitForLegFill(ctx, req)
	ratio := 0.0
	if fill != nil && req.Amount > 0 {
		ratio = fill.FilledAmount / req.Amount
	}
	confirmed := err == nil && fill != nil && fill.Filled && ratio >= closeFillThreshold
	outcome := executor.CloseOutcome{
		PositionID: req.PositionID, Leg: req.Leg, Venue: req.Venue,
		ClientOrderID: req.ClientOrderID, OrderID: orderID,
		RequestedAmount: req.Amount, Accepted: true, Confirmed: confirmed, Resolved: true,
	}
	if fill != nil {
		outcome.OrderID = fill.OrderID
		outcome.FilledAmount = fill.FilledAmount
		outcome.AvgFillPrice = fill.AvgFillPrice
		outcome.FillRatio = ratio
	}
	if err != nil {
		outcome.Error = "fill confirmation failed: " + err.Error()
	} else if !confirmed {
		outcome.Error = fmt.Sprintf("close fill %.1f%% below %.1f%% threshold", ratio*100, closeFillThreshold*100)
	}
	if err := s.liveStore.UpsertCloseOutcome(ctx, outcome); err != nil {
		s.liveStore.MarkCloseDegraded(ctx, req.PositionID)
		return
	}
	if !confirmed {
		s.markCloseFailed(ctx, req, outcome.Error)
		return
	}

	s.liveStore.InsertEvent(ctx, req.PositionID, "close_leg_fill", executor.ExecStateClosing,
		fmt.Sprintf("leg=%d venue=%s filled=%.8f ratio=%.1f%%", req.Leg, req.Venue, outcome.FilledAmount, ratio*100))
	progress, err := s.liveStore.GetCloseProgress(ctx, req.PositionID)
	if err != nil {
		s.logger.Error("live close: get progress", "err", err, "id", req.PositionID)
		s.liveStore.MarkCloseDegraded(ctx, req.PositionID)
		return
	}
	if progress.Required > 0 && progress.Confirmed == progress.Required {
		changed, err := s.liveStore.MarkClosed(ctx, req.PositionID)
		if err == nil && changed {
			s.liveStore.InsertEvent(ctx, req.PositionID, "close_complete", executor.ExecStateClosed,
				fmt.Sprintf("%d close fills confirmed", progress.Confirmed))
		}
	} else if progress.Failed == 0 && progress.Pending == 0 {
		go s.markIncompleteCloseAfterGrace(req.PositionID)
	}
}

func (s *Server) markIncompleteCloseAfterGrace(positionID string) {
	timer := time.NewTimer(closeSubmissionGrace)
	defer timer.Stop()
	select {
	case <-s.ctx.Done():
		return
	case <-timer.C:
	}
	progress, err := s.liveStore.GetCloseProgress(s.ctx, positionID)
	if err != nil || progress.Required == 0 || progress.Confirmed == progress.Required || progress.Failed > 0 || progress.Pending > 0 {
		return
	}
	if err := s.liveStore.MarkCloseDegraded(s.ctx, positionID); err != nil {
		return
	}
	s.liveStore.InsertEvent(s.ctx, positionID, "close_incomplete", executor.ExecStateDegraded,
		fmt.Sprintf("%d of %d close legs were submitted and confirmed", progress.Confirmed, progress.Required))
}

func (s *Server) markCloseFailed(ctx context.Context, req *domain.SigningRequest, reason string) {
	if reason == "" {
		reason = "close order rejected"
	}
	if err := s.liveStore.MarkCloseDegraded(ctx, req.PositionID); err != nil {
		return
	}
	s.liveStore.InsertEvent(ctx, req.PositionID, "close_leg_failed", executor.ExecStateDegraded,
		fmt.Sprintf("leg=%d venue=%s: %s", req.Leg, req.Venue, reason))
}
