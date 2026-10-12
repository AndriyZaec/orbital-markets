package api

import "context"

// Signed orders cannot be widened after approval. Verify depth for the actual
// normalized amount immediately before submitting the first opening order.
func (s *Server) validateFreshOpenDepth(ctx context.Context, session *LiveSession) error {
	if session.Plan == nil || session.Plan.Liquidity == nil {
		return nil
	}
	return s.scanner.ValidateExecutionLiquidity(ctx, session.Plan, liveSessionLeg1Amount(session))
}
