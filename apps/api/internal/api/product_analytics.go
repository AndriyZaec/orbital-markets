package api

import (
	"context"
	"strings"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/analytics"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/executor"
)

func (s *Server) trackLiveOpenOutcome(sess *LiveSession, state executor.ExecState) {
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

	s.productAnalytics.Track(event, "session:"+sess.ID, map[string]any{
		"asset":           sess.Plan.Asset,
		"venue_pair":      strings.ToLower(sess.Plan.Leg1.Venue + "_" + sess.Plan.Leg2.Venue),
		"risk_tier":       string(sess.Plan.RiskTier),
		"notional_bucket": analytics.NotionalBucket(sess.Plan.Notional),
	})
}

func (s *Server) trackLivePositionEvent(event string, position *executor.LivePosition, closeReason string) {
	if s.productAnalytics == nil || position == nil {
		return
	}
	properties := map[string]any{
		"asset":           position.Asset,
		"venue_pair":      strings.ToLower(position.VenueA + "_" + position.VenueB),
		"notional_bucket": analytics.NotionalBucket(position.Notional),
	}
	if closeReason != "" {
		properties["close_reason"] = closeReason
	}
	s.productAnalytics.Track(event, "position:"+position.ID, properties)
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
