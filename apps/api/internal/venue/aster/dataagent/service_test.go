package dataagent

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeVenue struct {
	approveErr     error
	report         Report
	approveCalls   atomic.Int32
	probeCalls     atomic.Int32
	approveStarted chan struct{}
	releaseApprove chan struct{}
	afterApprove   func()
	executionAgent string
	activeAgent    string
}

func (f *fakeVenue) Approve(context.Context, Approval, string) error {
	f.approveCalls.Add(1)
	if f.approveStarted != nil {
		select {
		case f.approveStarted <- struct{}{}:
		default:
		}
	}
	if f.releaseApprove != nil {
		<-f.releaseApprove
	}
	if f.afterApprove != nil {
		f.afterApprove()
	}
	return f.approveErr
}

func (f *fakeVenue) Probe(_ context.Context, _, _, executionAgent string, _ []byte, _ int64) Report {
	f.probeCalls.Add(1)
	f.executionAgent = executionAgent
	report := f.report
	if f.activeAgent != "" {
		report.ExecutionAgentPreserved = executionAgent == f.activeAgent
	}
	return report
}

func TestServiceReconcilesAcceptedExecutionAgentWithoutLocalRegistration(t *testing.T) {
	service, _, now := newLifecycleService(t)
	venue := &fakeVenue{report: successfulReport(now), activeAgent: testRotatedExecutionAgent}
	service.venue = venue
	prepared := prepareProbe(t, service)
	if err := service.Authorize(
		context.Background(), prepared.ProbeID, testSignature, testOwner, testExecutionAgent, prepared.Approval,
	); err != nil {
		t.Fatal(err)
	}

	agent, err := service.ReconcileExecutionAgent(context.Background(), testOwner, []string{
		testExecutionAgent, testRotatedExecutionAgent,
	})
	if err != nil || agent != testRotatedExecutionAgent || venue.probeCalls.Load() != 2 {
		t.Fatalf("agent = %q probes = %d err = %v", agent, venue.probeCalls.Load(), err)
	}
}

type fakeExecutionChecker struct {
	matches       bool
	expectedAgent string
}

func (f fakeExecutionChecker) AgentAuthorizationMatches(_ context.Context, _, _, agent string) (bool, error) {
	return f.matches && (f.expectedAgent == "" || agent == f.expectedAgent), nil
}

func TestServiceConcurrentValidateSubmitsApprovalExactlyOnce(t *testing.T) {
	service, store, now := newLifecycleService(t)
	venue := &fakeVenue{
		report: successfulReport(now), approveStarted: make(chan struct{}, 1), releaseApprove: make(chan struct{}),
	}
	service.venue = venue
	prepared := prepareProbe(t, service)
	results := make(chan error, 2)
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := service.Validate(context.Background(), prepared.ProbeID, testSignature, testOwner, testExecutionAgent)
			results <- err
		}()
	}
	<-venue.approveStarted
	close(venue.releaseApprove)
	wait.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if venue.approveCalls.Load() != 1 || successes != 1 {
		t.Fatalf("approval calls = %d, successful validates = %d", venue.approveCalls.Load(), successes)
	}
	status, err := store.StatusByOwner(context.Background(), testOwner)
	if err != nil || status.Status != StatusApproved {
		t.Fatalf("status = %+v, err = %v", status, err)
	}
}

func TestServiceRejectsStalePreparationWithoutSending(t *testing.T) {
	service, store, now := newLifecycleService(t)
	venue := &fakeVenue{}
	service.venue = venue
	prepared := prepareProbe(t, service)
	*now = now.Add(61 * time.Second)
	if _, err := service.Validate(context.Background(), prepared.ProbeID, testSignature, testOwner, testExecutionAgent); !errors.Is(err, ErrStale) {
		t.Fatalf("error = %v", err)
	}
	status, _ := store.StatusByOwner(context.Background(), testOwner)
	if venue.approveCalls.Load() != 0 || status.Status != StatusRejected {
		t.Fatalf("approval calls = %d, status = %+v", venue.approveCalls.Load(), status)
	}
}

func TestServiceRetainsAmbiguousAndRejectedApprovalOutcomes(t *testing.T) {
	for _, test := range []struct {
		name        string
		approvalErr error
		want        Status
	}{
		{"ambiguous", ErrApprovalAmbiguous, StatusUncertain},
		{"rejected", ErrApprovalRejected, StatusRejected},
		{"not sent", ErrApprovalNotSent, StatusRejected},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, store, _ := newLifecycleService(t)
			service.venue = &fakeVenue{approveErr: test.approvalErr}
			prepared := prepareProbe(t, service)
			if _, err := service.Validate(context.Background(), prepared.ProbeID, testSignature, testOwner, testExecutionAgent); err == nil {
				t.Fatal("approval failure was returned as success")
			}
			status, _ := store.StatusByOwner(context.Background(), testOwner)
			if status.Status != test.want {
				t.Fatalf("status = %s", status.Status)
			}
			_, prepareErr := service.Prepare(context.Background(), testOwner, testExecutionAgent)
			if test.want == StatusUncertain && !errors.Is(prepareErr, ErrUncertain) {
				t.Fatalf("uncertain outcome was replaceable: %v", prepareErr)
			}
			if test.want == StatusRejected && prepareErr != nil {
				t.Fatalf("rejected authorization was not repairable: %v", prepareErr)
			}
		})
	}
}

func TestServicePreparesUnifiedAuthorizationBeforeExecutionAgentExists(t *testing.T) {
	service, _, _ := newLifecycleService(t)
	service.execution = fakeExecutionChecker{matches: false}
	if _, err := service.Prepare(context.Background(), testOwner, testExecutionAgent); err != nil {
		t.Fatalf("prepare required an existing execution agent: %v", err)
	}
}

func TestServiceAllowsImmediateRetryAfterWalletCancellation(t *testing.T) {
	service, _, now := newLifecycleService(t)
	first := prepareProbe(t, service)
	*now = now.Add(time.Second)
	second, err := service.Prepare(context.Background(), testOwner, testRotatedExecutionAgent)
	if err != nil {
		t.Fatalf("retry prepare failed: %v", err)
	}
	if second.ProbeID != first.ProbeID || second.Approval.AgentAddress != first.Approval.AgentAddress ||
		second.Approval.Nonce == first.Approval.Nonce {
		t.Fatalf("retry did not reuse the unsubmitted credential: first = %+v, second = %+v", first, second)
	}
	if err := service.Authorize(
		context.Background(), second.ProbeID, testSignature, testOwner,
		testRotatedExecutionAgent, second.Approval,
	); err != nil {
		t.Fatalf("retry authorization failed: %v", err)
	}
}

func TestServiceReauthorizationReusesReadOnlyCredentialAfterAcceptance(t *testing.T) {
	service, store, now := newLifecycleService(t)
	venue := &fakeVenue{report: successfulReport(now)}
	service.venue = venue
	first := prepareProbe(t, service)
	if err := service.Authorize(
		context.Background(), first.ProbeID, testSignature, testOwner, testExecutionAgent, first.Approval,
	); err != nil {
		t.Fatal(err)
	}
	if err := service.Authorize(
		context.Background(), first.ProbeID, testSignature, testOwner, testExecutionAgent, first.Approval,
	); err != nil || venue.approveCalls.Load() != 1 {
		t.Fatalf("accepted authorization retry was not idempotent: calls = %d, err = %v", venue.approveCalls.Load(), err)
	}
	original, err := store.LoadByOwner(context.Background(), testOwner)
	if err != nil {
		t.Fatal(err)
	}
	originalKey := append([]byte(nil), original.PrivateKey...)
	clear(original.PrivateKey)
	*now = now.Add(time.Minute)

	prepared, err := service.Prepare(context.Background(), testOwner, testRotatedExecutionAgent)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Approval.AgentAddress != first.Approval.AgentAddress {
		t.Fatalf("data-agent address changed: %s", prepared.Approval.AgentAddress)
	}
	before, _ := store.LoadByOwner(context.Background(), testOwner)
	if before.ExecutionAgent != testExecutionAgent || before.RequestedExpiry != first.Approval.Expired {
		t.Fatalf("prepare mutated approved credential: %+v", before)
	}
	clear(before.PrivateKey)

	if err := service.Authorize(
		context.Background(), prepared.ProbeID, testSignature, testOwner,
		testRotatedExecutionAgent, prepared.Approval,
	); err != nil {
		t.Fatal(err)
	}
	after, err := store.LoadByOwner(context.Background(), testOwner)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(after.PrivateKey)
	if after.ExecutionAgent != testExecutionAgent || after.RequestedExpiry != prepared.Approval.Expired ||
		!bytes.Equal(after.PrivateKey, originalKey) || after.Status != StatusApproved {
		t.Fatalf("reauthorized credential = %+v", after)
	}
}

func TestServiceAmbiguousReauthorizationStopsFurtherRetries(t *testing.T) {
	service, store, now := newLifecycleService(t)
	venue := &fakeVenue{report: successfulReport(now)}
	service.venue = venue
	first := prepareProbe(t, service)
	if err := service.Authorize(
		context.Background(), first.ProbeID, testSignature, testOwner, testExecutionAgent, first.Approval,
	); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(time.Minute)
	prepared, err := service.Prepare(context.Background(), testOwner, testRotatedExecutionAgent)
	if err != nil {
		t.Fatal(err)
	}
	venue.approveErr = ErrApprovalAmbiguous
	if err := service.Authorize(
		context.Background(), prepared.ProbeID, testSignature, testOwner,
		testRotatedExecutionAgent, prepared.Approval,
	); !errors.Is(err, ErrUncertain) {
		t.Fatalf("error = %v", err)
	}
	status, err := store.StatusByOwner(context.Background(), testOwner)
	if err != nil || status.Status != StatusUncertain {
		t.Fatalf("status = %+v, err = %v", status, err)
	}
	if _, err := service.Prepare(
		context.Background(), testOwner, testRotatedExecutionAgent,
	); !errors.Is(err, ErrUncertain) {
		t.Fatalf("ambiguous authorization was retryable: %v", err)
	}
}

func TestServiceRejectedReauthorizationPreservesApprovedCredential(t *testing.T) {
	service, store, now := newLifecycleService(t)
	venue := &fakeVenue{report: successfulReport(now)}
	service.venue = venue
	first := prepareProbe(t, service)
	if err := service.Authorize(
		context.Background(), first.ProbeID, testSignature, testOwner, testExecutionAgent, first.Approval,
	); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(time.Minute)
	prepared, err := service.Prepare(context.Background(), testOwner, testRotatedExecutionAgent)
	if err != nil {
		t.Fatal(err)
	}
	venue.approveErr = ErrApprovalRejected
	if err := service.Authorize(
		context.Background(), prepared.ProbeID, testSignature, testOwner,
		testRotatedExecutionAgent, prepared.Approval,
	); !errors.Is(err, ErrRejected) {
		t.Fatalf("error = %v", err)
	}
	record, err := store.LoadByOwner(context.Background(), testOwner)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(record.PrivateKey)
	if record.Status != StatusApproved || record.ExecutionAgent != testExecutionAgent ||
		record.RequestedExpiry != first.Approval.Expired {
		t.Fatalf("rejected reauthorization mutated approved credential: %+v", record)
	}
}

func TestServiceKeepsApprovedStateWhenReadProbeFails(t *testing.T) {
	service, store, now := newLifecycleService(t)
	service.venue = &fakeVenue{report: Report{RequestedExpiry: now.Add(agentLifetime).UnixMilli(), Error: "Aster account response was invalid"}}
	prepared := prepareProbe(t, service)
	report, err := service.Validate(context.Background(), prepared.ProbeID, testSignature, testOwner, testExecutionAgent)
	if err != nil || report.Success || report.Error == "" {
		t.Fatalf("report = %+v, err = %v", report, err)
	}
	status, _ := store.StatusByOwner(context.Background(), testOwner)
	if status.Status != StatusApproved || status.LastResult == nil || status.LastError == "" {
		t.Fatalf("status = %+v", status)
	}
}

func TestServiceReportsMissingVenueExecutionAgent(t *testing.T) {
	service, store, now := newLifecycleService(t)
	service.venue = &fakeVenue{report: Report{
		RequestedExpiry: now.Add(agentLifetime).UnixMilli(), ReportedExpiry: now.Add(agentLifetime).UnixMilli(),
		MatchedAgentPermissions: &AgentPermissions{CanRead: true}, Error: "expected Aster execution agent is missing",
	}}
	prepared := prepareProbe(t, service)
	report, err := service.Validate(context.Background(), prepared.ProbeID, testSignature, testOwner, testExecutionAgent)
	if err != nil || report.ExecutionAgentPreserved || report.Success {
		t.Fatalf("report = %+v, err = %v", report, err)
	}
	status, _ := store.StatusByOwner(context.Background(), testOwner)
	if status.Status != StatusApproved {
		t.Fatalf("status = %+v", status)
	}
}

func TestServiceRunsApprovedProbeAfterRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agents.db")
	key := bytes.Repeat([]byte{8}, 32)
	now := time.Unix(1_800_000_000, 0)
	database := openAgentDB(t, path)
	store, _ := NewStore(database, key)
	firstVenue := &fakeVenue{report: successfulReport(&now)}
	first := NewService(store, firstVenue, fakeExecutionChecker{matches: true}, func() time.Time { return now })
	prepared := prepareProbe(t, first)
	if _, err := first.Validate(context.Background(), prepared.ProbeID, testSignature, testOwner, testExecutionAgent); err != nil {
		t.Fatal(err)
	}
	database.Close()

	database = openAgentDB(t, path)
	defer database.Close()
	restartedStore, _ := NewStore(database, key)
	restartedVenue := &fakeVenue{report: successfulReport(&now)}
	restarted := NewService(restartedStore, restartedVenue, fakeExecutionChecker{matches: true}, func() time.Time { return now })
	report, err := restarted.Run(context.Background(), testOwner, testExecutionAgent)
	if err != nil || !report.Success || restartedVenue.probeCalls.Load() != 1 || restartedVenue.executionAgent != testExecutionAgent {
		t.Fatalf("report = %+v, calls = %d, execution = %s, err = %v", report, restartedVenue.probeCalls.Load(), restartedVenue.executionAgent, err)
	}
}

func TestServiceUsesCurrentExecutionAgentAfterRotationAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agents.db")
	key := bytes.Repeat([]byte{0x31}, 32)
	now := time.Unix(1_800_000_000, 0)
	database := openAgentDB(t, path)
	store, _ := NewStore(database, key)
	initialVenue := &fakeVenue{report: successfulReport(&now)}
	initial := NewService(store, initialVenue, fakeExecutionChecker{
		matches: true, expectedAgent: testExecutionAgent,
	}, func() time.Time { return now })
	prepared := prepareProbe(t, initial)
	if prepared.Approval.AgentName != "OrbitalData" {
		t.Fatalf("agent name = %q", prepared.Approval.AgentName)
	}
	if _, err := initial.Validate(context.Background(), prepared.ProbeID, testSignature, testOwner, testExecutionAgent); err != nil {
		t.Fatal(err)
	}
	if initialVenue.executionAgent != testExecutionAgent {
		t.Fatalf("initial venue execution agent = %s", initialVenue.executionAgent)
	}
	database.Close()

	database = openAgentDB(t, path)
	defer database.Close()
	restartedStore, _ := NewStore(database, key)
	rotatedVenue := &fakeVenue{report: successfulReport(&now)}
	restarted := NewService(restartedStore, rotatedVenue, fakeExecutionChecker{
		matches: true, expectedAgent: testRotatedExecutionAgent,
	}, func() time.Time { return now })
	status, err := restarted.Status(context.Background(), testOwner, testRotatedExecutionAgent)
	if err != nil || status.Status != StatusApproved {
		t.Fatalf("status = %+v, err = %v", status, err)
	}
	report, err := restarted.Run(context.Background(), testOwner, testRotatedExecutionAgent)
	if err != nil || !report.Success || rotatedVenue.executionAgent != testRotatedExecutionAgent {
		t.Fatalf("report = %+v, venue execution agent = %s, err = %v", report, rotatedVenue.executionAgent, err)
	}
	for _, operation := range []func() error{
		func() error {
			_, err := restarted.Status(context.Background(), testOwner, testUnauthorizedExecutionAgent)
			return err
		},
		func() error {
			_, err := restarted.Run(context.Background(), testOwner, testUnauthorizedExecutionAgent)
			return err
		},
	} {
		if err := operation(); !errors.Is(err, ErrExecutionAgentMismatch) {
			t.Fatalf("unauthorized rotated execution agent error = %v", err)
		}
	}
}

func TestServiceRunRejectsMismatchedLocalExecutionAuthorization(t *testing.T) {
	service, _, now := newLifecycleService(t)
	venue := &fakeVenue{report: successfulReport(now)}
	service.venue = venue
	prepared := prepareProbe(t, service)
	if _, err := service.Validate(context.Background(), prepared.ProbeID, testSignature, testOwner, testExecutionAgent); err != nil {
		t.Fatal(err)
	}
	service.execution = fakeExecutionChecker{matches: false}
	if _, err := service.Run(context.Background(), testOwner, testExecutionAgent); !errors.Is(err, ErrExecutionAgentMismatch) {
		t.Fatalf("error = %v", err)
	}
	if venue.probeCalls.Load() != 1 {
		t.Fatalf("run reached venue with mismatched local authorization; calls = %d", venue.probeCalls.Load())
	}
}

func TestServicePersistsApprovalAfterRequestCancellation(t *testing.T) {
	service, store, now := newLifecycleService(t)
	ctx, cancel := context.WithCancel(context.Background())
	service.venue = &fakeVenue{report: successfulReport(now), afterApprove: cancel}
	prepared := prepareProbe(t, service)
	if _, err := service.Validate(ctx, prepared.ProbeID, testSignature, testOwner, testExecutionAgent); err == nil {
		t.Fatal("canceled read probe unexpectedly succeeded")
	}
	status, err := store.StatusByOwner(context.Background(), testOwner)
	if err != nil || status.Status != StatusApproved {
		t.Fatalf("status = %+v, err = %v", status, err)
	}
}

func newLifecycleService(t *testing.T) (*Service, *Store, *time.Time) {
	t.Helper()
	database := openAgentDB(t, filepath.Join(t.TempDir(), "agents.db"))
	t.Cleanup(func() { database.Close() })
	store, err := NewStore(database, bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	service := NewService(store, &fakeVenue{}, fakeExecutionChecker{matches: true}, func() time.Time { return now })
	return service, store, &now
}

func prepareProbe(t *testing.T, service *Service) Prepared {
	t.Helper()
	prepared, err := service.Prepare(context.Background(), testOwner, testExecutionAgent)
	if err != nil {
		t.Fatal(err)
	}
	return prepared
}

func successfulReport(now *time.Time) Report {
	return Report{
		Endpoints:               EndpointReport{Agent: true, Account: true, PositionRisk: true, Income: true},
		MatchedAgentPermissions: &AgentPermissions{CanRead: true}, RequestedExpiry: now.Add(agentLifetime).UnixMilli(),
		ReportedExpiry: now.Add(agentLifetime).UnixMilli(), ExecutionAgentPreserved: true, Success: true,
	}
}

const testSignature = "0x111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111b"
