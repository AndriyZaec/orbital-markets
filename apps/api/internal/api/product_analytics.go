package api

import (
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
