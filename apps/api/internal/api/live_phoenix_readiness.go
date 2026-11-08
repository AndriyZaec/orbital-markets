package api

import (
	"context"
	"time"

	phoenixruntime "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/runtime"
)

// Diagnostic readiness is not order admission: prepare and submit still verify
// current chain evidence, exact intent, risk projection and signed fee policy.
type phoenixExecutionBackend interface {
	PhoenixSubmitBackend
	PhoenixInfrastructure
	FlightReadiness() phoenixruntime.FlightStatus
}

func (f *phoenixAccountFeedFactory) executionBackend() (phoenixExecutionBackend, bool) {
	b, ok := f.config.Backend.(phoenixExecutionBackend)
	return b, ok && f.orders != nil && f.orderJournal != nil
}

func (f *phoenixAccountFeed) checkExecutionReadiness(ctx context.Context, details *phoenixAccountDetails) {
	details.executionCheckedAt = time.Now()
	b, ok := f.factory.executionBackend()
	if !ok {
		details.executionReason = errPhoenixLiveDisabled.Error()
		return
	}
	status := b.Readiness(ctx)
	if !status.Ready {
		details.executionReason = "Phoenix infrastructure not ready: " + status.Reason
		return
	}
	pending, err := f.factory.orderJournal.Unresolved(ctx, f.state.Snapshot().Owner)
	if err != nil || pending {
		details.executionReason = "Phoenix prior order outcome requires reconciliation"
	}
}

func (f *phoenixAccountFeed) executionReadiness(details *phoenixAccountDetails, accountReason string) string {
	if accountReason != "" {
		return accountReason
	}
	b, ok := f.factory.executionBackend()
	if !ok {
		return errPhoenixLiveDisabled.Error()
	}
	if !details.AccountReady || !details.Authorized {
		return "Phoenix account authorization required"
	}
	if !details.CanIncreaseRisk {
		return "Phoenix account cannot increase risk"
	}
	if !phoenixTimeFresh(details.executionCheckedAt, time.Now(), admissionFreshness) {
		return "Phoenix infrastructure verification is stale"
	}
	if details.executionReason != "" {
		return details.executionReason
	}
	flight := b.FlightReadiness()
	if !flight.Ready {
		return "Phoenix Flight registration not ready"
	}
	return ""
}

func (s *Server) phoenixExecutionConfigured() bool {
	if s.live == nil || s.live.accounts == nil {
		return false
	}
	if _, err := s.live.liveModule("phoenix"); err != nil {
		return false
	}
	f, ok := s.live.accounts.factories["phoenix"].(*phoenixAccountFeedFactory)
	if !ok {
		return false
	}
	_, ok = f.executionBackend()
	return ok
}
