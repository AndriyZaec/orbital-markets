package api

import (
	"context"
	"strings"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/analytics"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/executor"
)

func (s *Server) trackLiveOpenOutcome(sess *LiveSession, state executor.ExecState, reasons ...string) {
	if s.productAnalytics == nil || sess == nil || sess.Plan == nil {
		return
	}

	event := ""
	switch state {
	case executor.ExecStateOpen:
		event = "live_open_succeeded"
	case executor.ExecStateFailed:
		event = "live_open_failed"
	case executor.ExecStateDegraded:
		event = "position_degraded"
	default:
		return
	}

	properties := map[string]any{
		"asset":      sess.Plan.Asset,
		"venue_pair": strings.ToLower(sess.Plan.Leg1.Venue + "_" + sess.Plan.Leg2.Venue),
	}
	switch event {
	case "live_open_succeeded":
		properties["risk_tier"] = string(sess.Plan.RiskTier)
		properties["notional_bucket"] = analytics.NotionalBucket(sess.Plan.Notional)
	case "live_open_failed":
		properties["failure_reason"] = analyticsFailureReason(reasons...)
	case "position_degraded":
		properties["failure_reason"] = analyticsFailureReason(reasons...)
	}
	s.productAnalytics.Track(event, "session:"+sess.ID, properties)
}

func (s *Server) trackLivePositionEvent(event string, position *executor.LivePosition, closeReason string) {
	if s.productAnalytics == nil || position == nil {
		return
	}
	properties := map[string]any{
		"asset":      position.Asset,
		"venue_pair": strings.ToLower(position.VenueA + "_" + position.VenueB),
	}
	if closeReason != "" {
		if event == "position_degraded" {
			properties["failure_reason"] = closeReason
		} else if event == "position_closed" {
			properties["close_reason"] = closeReason
		}
	}
	s.productAnalytics.Track(event, "position:"+position.ID, properties)
}

func analyticsFailureReason(reasons ...string) string {
	reason := strings.TrimSpace(strings.Join(reasons, "; "))
	if reason == "" {
		return "unknown"
	}
	return reason
}

func (s *Server) markCloseDegraded(ctx context.Context, positionID string) error {
	previous, previousErr := s.liveStore.GetPosition(ctx, positionID)
	if err := s.liveStore.MarkCloseDegraded(ctx, positionID); err != nil {
		return err
	}
	if previousErr == nil && (previous.State == string(executor.ExecStateDegraded) || previous.State == string(executor.ExecStateClosed)) {
		return nil
	}
	position, err := s.liveStore.GetPosition(ctx, positionID)
	if err != nil {
		s.logger.Warn("product analytics: load degraded position", "err", err, "position_id", positionID)
		return nil
	}
	s.trackLivePositionEvent("position_degraded", position, "close_recovery")
	return nil
}

func (s *Server) trackLivePositionClosed(position *executor.LivePosition, reason string) {
	s.trackLivePositionEvent("position_closed", position, reason)
}
