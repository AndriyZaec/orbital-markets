package api

import (
	"context"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
)

type exactOrderReconciler interface {
	ReconcileOrder(context.Context, *domain.SigningRequest) (*normFill, error)
}

func (s *Server) firePhoenixUnwind(ctx context.Context, session *LiveSession) unwindResult {
	request := session.ArmedUnwindReq
	result := unwindResult{RequestedAmount: request.Amount, Status: unwindUnconfirmed}
	if request.Action != "unwind" || !request.ReduceOnly || !positiveLiveNumber(request.Amount) {
		result.Status = unwindNeedsSignature
		return result
	}
	if fillPresent(session.Leg1Fill) {
		result.RequestedAmount = session.Leg1Fill.FilledAmount
	}
	feed, ok := session.accounts.Feed("phoenix")
	if !ok {
		return result
	}
	source, ok := feed.(exactOrderReconciler)
	if !ok {
		return result
	}
	interpret := func(fill *normFill) unwindResult {
		if fill != nil {
			result.FilledAmount = fill.FilledAmount
		}
		if fillPresent(session.Leg1Fill) && fillPresent(fill) && unwindFullyFilled(result.RequestedAmount, fill.FilledAmount) {
			result.Status = unwindConfirmed
		}
		if fill != nil && !fillPresent(fill) && (fill.Status == "failed" || fill.Status == "no_fill") {
			result.Status = unwindNeedsSignature
		}
		return result
	}
	// Reconcile first, including after wall-clock/native expiry. A previously
	// submitted signature is never replaced or broadcast a second time.
	prior, err := source.ReconcileOrder(ctx, request)
	if err != nil || prior == nil {
		return result
	}
	if prior.Status != "not_submitted" {
		return interpret(prior)
	}
	if session.ArmedUnwindSigned == nil {
		result.Status = unwindNotArmed
		return result
	}
	// Reduce-only clamps to total owner exposure, not this strategy's fill.
	// Never let a larger pre-signed recovery order consume existing positions.
	if !fillPresent(session.Leg1Fill) || request.Amount > session.Leg1Fill.FilledAmount || !time.Now().Before(request.ExpiresAt) {
		result.Status = unwindNeedsSignature
		return result
	}
	if pending, err := recoveryOrdersUnresolved(ctx, session.accounts); err != nil || pending {
		return result
	}
	sub, err := s.submitSignedActionForAccounts(ctx, *session.ArmedUnwindSigned, request, session.accounts)
	if err != nil || sub == nil {
		if fill, lookupErr := source.ReconcileOrder(ctx, request); lookupErr == nil && fill != nil && fill.Status != "not_submitted" {
			return interpret(fill)
		}
		return result
	}
	if !sub.Accepted {
		result.Status = unwindNeedsSignature
		return result
	}
	fill, err := s.waitForLegFillForAccounts(ctx, request, session.accounts)
	if err != nil {
		return result
	}
	return interpret(fill)
}
