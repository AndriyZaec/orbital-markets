package api

// Readiness permits starting preparation, not submission. No background RPC is
// needed: prepare and submit independently verify all current chain evidence.
func (f *phoenixAccountFeedFactory) executionBackend() (PhoenixSubmitBackend, bool) {
	b, ok := f.config.Backend.(PhoenixSubmitBackend)
	return b, ok && f.orders != nil && f.orderJournal != nil
}

func (f *phoenixAccountFeed) executionReadiness(details *phoenixAccountDetails, accountReason string) string {
	if accountReason != "" {
		return accountReason
	}
	b, ok := f.factory.executionBackend()
	if !ok {
		return errPhoenixLiveDisabled.Error()
	}
	if !details.AccountReady || !details.AuthorizationRecorded {
		return "Phoenix account authorization required"
	}
	if !details.CanIncreaseRisk {
		return "Phoenix account cannot increase risk"
	}
	if details.orderOutcomeUnknown {
		return "Phoenix prior order outcome requires reconciliation"
	}
	if b.BuilderAuthority() == "" {
		return "Phoenix Flight builder is not configured"
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
	b, ok := f.executionBackend()
	return ok && b.BuilderAuthority() != ""
}
