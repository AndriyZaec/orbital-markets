package api

import (
	"context"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
)

func (s *Server) recordHedgeRejection(ctx context.Context, session *LiveSession, request *domain.SigningRequest, attempt int, result *domain.SubmissionResult) {
	reason := result.PrivateDiagnostic
	if reason == "" {
		reason = result.Error
	}
	diagnostic := submissionRejection{Venue: request.Venue, RequestID: request.ID,
		ClientOrderID: request.ClientOrderID, Attempt: attempt, Reason: reason}
	session.SubmissionRejections = append(session.SubmissionRejections, diagnostic)
	s.logger.Warn("live advance: hedge submission rejected", "session_id", session.ID,
		"venue", diagnostic.Venue, "request_id", diagnostic.RequestID,
		"client_order_id", diagnostic.ClientOrderID, "attempt", attempt, "reason", diagnostic.Reason)
	// Keep adapter reasons in the private durable payload, not public reasons or
	// recovery events. A diagnostic write failure must not prevent safety recovery.
	if err := s.saveLiveSession(ctx, session); err != nil {
		s.logger.Error("live advance: hedge rejection diagnostic persistence failed", "session_id", session.ID, "err", err)
	}
}
