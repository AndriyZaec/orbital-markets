package api

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
	asteraccount "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/aster/account"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/aster/dataagent"
	asterlive "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/aster/live"
	pacaccount "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/pacifica/account"
)

type fakeAsterAccountClient struct {
	order   *domain.SubmissionResult
	private *asterlive.PrivateResult
	fill    *asterlive.FillResult
}

type fakeAsterAccountReader struct {
	mu    sync.Mutex
	calls int
	read  func(context.Context, string, int) (asteraccount.Observation, error)
}

type fakeAsterFundingReader struct {
	mu    sync.Mutex
	calls int
	read  func(context.Context, string, time.Time, time.Time, int) ([]venue.FundingPayment, error)
}

func (f *fakeAsterFundingReader) ReadFunding(
	ctx context.Context,
	owner string,
	since, until time.Time,
) ([]venue.FundingPayment, error) {
	f.mu.Lock()
	f.calls++
	call := f.calls
	f.mu.Unlock()
	return f.read(ctx, owner, since, until, call)
}

func (f *fakeAsterFundingReader) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeAsterAccountReader) ReadAccount(ctx context.Context, owner string) (asteraccount.Observation, error) {
	f.mu.Lock()
	f.calls++
	call := f.calls
	f.mu.Unlock()
	return f.read(ctx, owner, call)
}

func (f *fakeAsterAccountReader) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func completeAsterObservation(observedAt time.Time) asteraccount.Observation {
	return asteraccount.Observation{
		DataAgent: "0x2222222222222222222222222222222222222222",
		Margin:    asteraccount.MarginSummary{CanTrade: true, Equity: 120, Available: 110},
		Positions: []asteraccount.Position{}, PositionMode: asteraccount.PositionMode{OneWay: true},
		LeverageBrackets: asteraccount.LeverageBrackets{}, ObservedAt: observedAt,
	}
}

func (f *fakeAsterAccountClient) SubmitSignedOrder(
	context.Context, domain.SignedAction, *domain.SigningRequest,
) (*domain.SubmissionResult, error) {
	return f.order, nil
}

func (f *fakeAsterAccountClient) SubmitSignedPrivate(
	context.Context, domain.SignedAction, *domain.SigningRequest,
) (*asterlive.PrivateResult, error) {
	return f.private, nil
}

func (f *fakeAsterAccountClient) WaitForFill(context.Context, string, string) (*asterlive.FillResult, error) {
	return f.fill, nil
}

func TestVenueAccountNormalization(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	pacifica := &pacificaAccountFeedFactory{logger: logger}
	hyperliquid := &hyperliquidAccountFeedFactory{logger: logger}

	if got, err := pacifica.Normalize("  SolCaseSensitive  "); err != nil || got != "SolCaseSensitive" {
		t.Fatalf("Pacifica normalization = %q, %v", got, err)
	}
	if got, err := hyperliquid.Normalize("  0xAbCd  "); err != nil || got != "0xabcd" {
		t.Fatalf("Hyperliquid normalization = %q, %v", got, err)
	}
}

func TestSigningAccountUsesVenueNormalization(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	registry := newAccountFeedRegistry(ctx, map[string]accountFeedFactory{
		"pacifica":    &pacificaAccountFeedFactory{logger: logger},
		"hyperliquid": &hyperliquidAccountFeedFactory{logger: logger},
	}, accountFeedRegistryConfig{})
	live := &LiveDeps{accounts: registry}

	if err := live.validateSigningAccount(&domain.SigningRequest{
		Venue: "hyperliquid", Account: "0xOwner", Signer: "0xAbCd",
	}, "0xabcd"); err != nil {
		t.Fatalf("case-insensitive Hyperliquid signer rejected: %v", err)
	}
	if err := live.validateSigningAccount(&domain.SigningRequest{
		Venue: "pacifica", Account: "Owner", Signer: "SolCaseSensitive",
	}, "solcasesensitive"); err == nil {
		t.Fatal("case-mismatched Pacifica signer accepted")
	}
}

func TestAgentIdentityRequiresVenueAddressFormat(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	registry := newAccountFeedRegistry(ctx, map[string]accountFeedFactory{
		"pacifica":    &pacificaAccountFeedFactory{logger: logger},
		"hyperliquid": &hyperliquidAccountFeedFactory{logger: logger},
	}, accountFeedRegistryConfig{})
	live := &LiveDeps{accounts: registry}

	if err := live.validateAgentIdentity("pacifica", "owner", "not-base58"); err == nil {
		t.Fatal("invalid Pacifica agent accepted")
	}
	if err := live.validateAgentIdentity("hyperliquid", "0xowner", "0x1234"); err == nil {
		t.Fatal("invalid Hyperliquid agent accepted")
	}
	if err := live.validateAgentIdentity(
		"pacifica", "owner", "3ogUn1GNXoASaRbxPNeVJnVv5rG4EPBtmQmX61jVorUe",
	); err != nil {
		t.Fatalf("valid Pacifica agent rejected: %v", err)
	}
}

func TestPacificaAcceptedLeverageUpdatesLocalAccountState(t *testing.T) {
	state := pacaccount.NewAccountState()
	state.ResetForAccount("owner")
	request := &domain.SigningRequest{
		ID: "leverage", ClientOrderID: "leverage", Venue: "pacifica", Action: "update_leverage",
		Account: "owner", Symbol: "VIRTUAL", Leverage: 2,
	}

	applyAcceptedPacificaLeverage(state, request)
	if got := state.Snapshot().SymbolConfigs["VIRTUAL"].Leverage; got != 2 {
		t.Fatalf("leverage = %v, want 2", got)
	}
}

func TestAsterAccountFeedSubmitsOrdersAndReturnsFill(t *testing.T) {
	client := &fakeAsterAccountClient{
		order: &domain.SubmissionResult{Venue: "aster", Accepted: true},
		fill: &asterlive.FillResult{
			OrderID: "42", ClientOrderID: "client-id", Status: "partial_fill",
			FilledAmount: 0.5, AvgFillPrice: 100, Filled: true,
		},
	}
	feed := &asterAccountFeed{state: asteraccount.NewAccountState("0xowner"), client: client}
	request := &domain.SigningRequest{
		Venue: "aster", Action: "open", Account: "0xowner", ClientOrderID: "client-id",
	}
	result, err := feed.SubmitSigned(context.Background(), domain.SignedAction{}, request)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || !result.Accepted {
		t.Fatalf("submission result = %+v", result)
	}
	fill, err := feed.WaitForFill(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !fill.Filled || fill.OrderID != "42" || fill.FilledAmount != 0.5 || fill.AvgFillPrice != 100 {
		t.Fatalf("fill = %+v", fill)
	}
}

func TestAsterAccountFeedPollsImmediatelyAndPeriodically(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := make(chan struct{}, 2)
	reader := &fakeAsterAccountReader{read: func(_ context.Context, owner string, call int) (asteraccount.Observation, error) {
		if owner != "0x1111111111111111111111111111111111111111" {
			t.Fatalf("owner = %q", owner)
		}
		calls <- struct{}{}
		return completeAsterObservation(time.Now().Add(time.Duration(call) * time.Millisecond)), nil
	}}
	factory := &asterAccountFeedFactory{reader: reader, pollInterval: 10 * time.Millisecond}
	feed, err := factory.Start(ctx, "0x1111111111111111111111111111111111111111")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		select {
		case <-calls:
		case <-time.After(time.Second):
			t.Fatal("Aster account poll did not run")
		}
	}
	if snapshot := feed.Snapshot(); !snapshot.Connected || snapshot.Equity != 120 {
		t.Fatalf("polled snapshot = %+v", snapshot)
	}
}

func TestAsterAccountFeedCoalescesExplicitRefresh(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	reader := &fakeAsterAccountReader{read: func(ctx context.Context, _ string, _ int) (asteraccount.Observation, error) {
		close(started)
		select {
		case <-ctx.Done():
			return asteraccount.Observation{}, ctx.Err()
		case <-release:
			return completeAsterObservation(time.Now()), nil
		}
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	feed := &asterAccountFeed{
		state:   asteraccount.NewAccountState("0x1111111111111111111111111111111111111111"),
		account: "0x1111111111111111111111111111111111111111", reader: reader, ctx: ctx,
	}
	errs := make(chan error, 2)
	go func() { errs <- feed.RefreshPositions(context.Background()) }()
	<-started
	go func() { errs <- feed.RefreshPositions(context.Background()) }()
	time.Sleep(10 * time.Millisecond)
	close(release)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if reader.callCount() != 1 {
		t.Fatalf("read calls = %d, want 1", reader.callCount())
	}
}

func TestAsterAccountFeedKeepsLastSnapshotAfterRefreshFailure(t *testing.T) {
	reader := &fakeAsterAccountReader{read: func(_ context.Context, _ string, call int) (asteraccount.Observation, error) {
		if call == 1 {
			return completeAsterObservation(time.Now()), nil
		}
		return asteraccount.Observation{}, errors.New("temporary read failure")
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	feed := &asterAccountFeed{
		state:   asteraccount.NewAccountState("0x1111111111111111111111111111111111111111"),
		account: "0x1111111111111111111111111111111111111111", reader: reader, ctx: ctx,
	}
	if err := feed.RefreshPositions(context.Background()); err != nil {
		t.Fatal(err)
	}
	before := feed.Snapshot()
	if err := feed.RefreshPositions(context.Background()); err == nil {
		t.Fatal("failed refresh returned no error")
	}
	after := feed.Snapshot()
	if after.LastUpdated != before.LastUpdated || after.Equity != before.Equity || !after.Connected {
		t.Fatalf("failed refresh changed snapshot: before=%+v after=%+v", before, after)
	}
}

func TestAsterAccountFeedReportsMissingReadAuthorization(t *testing.T) {
	reader := &fakeAsterAccountReader{read: func(context.Context, string, int) (asteraccount.Observation, error) {
		return asteraccount.Observation{}, dataagent.ErrNotApproved
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	feed := &asterAccountFeed{
		state:   asteraccount.NewAccountState("0x1111111111111111111111111111111111111111"),
		account: "0x1111111111111111111111111111111111111111", reader: reader, ctx: ctx,
	}
	if err := feed.RefreshPositions(context.Background()); !errors.Is(err, dataagent.ErrNotApproved) {
		t.Fatalf("refresh error = %v", err)
	}
	if snapshot := feed.Snapshot(); snapshot.UnavailableReason != "Aster read authorization required" {
		t.Fatalf("unavailable snapshot = %+v", snapshot)
	}
}

func TestAsterAccountFeedReportsExpiredAuthorizationAfterSnapshotStales(t *testing.T) {
	reader := &fakeAsterAccountReader{read: func(_ context.Context, _ string, call int) (asteraccount.Observation, error) {
		if call == 1 {
			return completeAsterObservation(time.Now().Add(-31 * time.Second)), nil
		}
		return asteraccount.Observation{}, dataagent.ErrNotApproved
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	feed := &asterAccountFeed{
		state:   asteraccount.NewAccountState("0x1111111111111111111111111111111111111111"),
		account: "0x1111111111111111111111111111111111111111", reader: reader, ctx: ctx, backendReads: true,
	}
	if err := feed.RefreshPositions(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := feed.RefreshPositions(context.Background()); !errors.Is(err, dataagent.ErrNotApproved) {
		t.Fatalf("refresh error = %v", err)
	}
	if snapshot := feed.Snapshot(); snapshot.UnavailableReason != "Aster read authorization required" {
		t.Fatalf("expired authorization snapshot = %+v", snapshot)
	}
}

func TestAsterAccountFeedDoesNotUseBrowserReadsAsBackendFallback(t *testing.T) {
	feed := &asterAccountFeed{
		state:        asteraccount.NewAccountState("0x1111111111111111111111111111111111111111"),
		backendReads: true,
	}
	positions := []asteraccount.Position{{Symbol: "BTCUSDT", Side: "long", Size: 1}}
	applied, err := feed.ApplyPrivateResult(&domain.SigningRequest{
		Account: "0x1111111111111111111111111111111111111111", Signer: "0xexecution",
		SnapshotID: "browser-1", CreatedAt: time.Now(),
	}, &asterlive.PrivateResult{
		AccountUpdate: &asteraccount.Update{SnapshotPart: &asteraccount.SnapshotPart{Positions: &positions}},
		RespondedAt:   time.Now(),
	})
	if err != nil || !applied {
		t.Fatalf("applied = %v, error = %v", applied, err)
	}
	if snapshot := feed.Snapshot(); len(snapshot.Positions) != 0 || !snapshot.LastUpdated.IsZero() ||
		snapshot.UnavailableReason != "Aster backend account reader is not configured" {
		t.Fatalf("browser read became backend fallback: %+v", snapshot)
	}
}

func TestAsterAccountFeedCancelsInflightPoll(t *testing.T) {
	started := make(chan struct{})
	stopped := make(chan struct{})
	reader := &fakeAsterAccountReader{read: func(ctx context.Context, _ string, _ int) (asteraccount.Observation, error) {
		close(started)
		<-ctx.Done()
		close(stopped)
		return asteraccount.Observation{}, ctx.Err()
	}}
	ctx, cancel := context.WithCancel(context.Background())
	factory := &asterAccountFeedFactory{reader: reader}
	if _, err := factory.Start(ctx, "0x1111111111111111111111111111111111111111"); err != nil {
		t.Fatal(err)
	}
	<-started
	cancel()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("in-flight Aster account poll was not cancelled")
	}
}

func TestAsterFundingFeedPollsOncePerOwnerAndAppliesSuccessfulEmptyReads(t *testing.T) {
	applied := make(chan struct{}, 2)
	reader := &fakeAsterFundingReader{read: func(
		_ context.Context, owner string, since, until time.Time, _ int,
	) ([]venue.FundingPayment, error) {
		if owner != "0x1111111111111111111111111111111111111111" {
			t.Fatalf("owner = %q", owner)
		}
		if got := until.Sub(since); got != asterFundingReadLookback {
			t.Fatalf("funding range = %v", got)
		}
		return []venue.FundingPayment{}, nil
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	registry := newAccountFeedRegistry(ctx, map[string]accountFeedFactory{
		"aster": &asterAccountFeedFactory{
			fundingReader: reader,
			applyFunding: func(context.Context, string, []venue.FundingPayment, time.Time, time.Time) error {
				applied <- struct{}{}
				return nil
			},
			fundingPollInterval: 100 * time.Millisecond,
		},
	}, accountFeedRegistryConfig{})
	first, err := registry.Acquire("aster", "0x1111111111111111111111111111111111111111")
	if err != nil {
		t.Fatal(err)
	}
	second, err := registry.Acquire("aster", "0x1111111111111111111111111111111111111111")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Release()
	defer second.Release()
	for i := 0; i < 2; i++ {
		select {
		case <-applied:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for Aster funding poll")
		}
	}
	if calls := reader.callCount(); calls != 2 {
		t.Fatalf("funding calls = %d, want one immediate and one periodic owner read", calls)
	}
}

func TestAsterFundingFeedDoesNotApplyFailedReadAndRetries(t *testing.T) {
	applied := make(chan struct{}, 1)
	reader := &fakeAsterFundingReader{read: func(
		_ context.Context, _ string, _ time.Time, _ time.Time, call int,
	) ([]venue.FundingPayment, error) {
		if call == 1 {
			return nil, errors.New("temporary failure")
		}
		return []venue.FundingPayment{}, nil
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	factory := &asterAccountFeedFactory{
		fundingReader: reader,
		applyFunding: func(context.Context, string, []venue.FundingPayment, time.Time, time.Time) error {
			applied <- struct{}{}
			return nil
		},
		fundingPollInterval: 100 * time.Millisecond,
	}
	if _, err := factory.Start(ctx, "0x1111111111111111111111111111111111111111"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-applied:
	case <-time.After(time.Second):
		t.Fatal("successful retry did not apply funding")
	}
	if calls := reader.callCount(); calls != 2 {
		t.Fatalf("funding calls = %d, want failed read and one retry", calls)
	}
}

func TestAsterFundingFeedCoalescesRefreshAndCancelsWithFeed(t *testing.T) {
	started := make(chan struct{})
	reader := &fakeAsterFundingReader{read: func(
		ctx context.Context, _ string, _ time.Time, _ time.Time, _ int,
	) ([]venue.FundingPayment, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	feedCtx, cancelFeed := context.WithCancel(context.Background())
	feed := &asterAccountFeed{
		account:       "0x1111111111111111111111111111111111111111",
		ctx:           feedCtx,
		fundingReader: reader,
		applyFunding: func(context.Context, string, []venue.FundingPayment, time.Time, time.Time) error {
			t.Fatal("failed funding read was applied")
			return nil
		},
	}
	first := feed.startFundingRefresh()
	<-started
	second := feed.startFundingRefresh()
	if first != second {
		t.Fatal("concurrent funding refresh was not coalesced")
	}
	cancelFeed()
	<-first.done
	if !errors.Is(first.err, context.Canceled) {
		t.Fatalf("refresh error = %v", first.err)
	}
	if calls := reader.callCount(); calls != 1 {
		t.Fatalf("funding calls = %d, want one coalesced read", calls)
	}
}

func TestAsterAccountFeedAppliesLeverageResponse(t *testing.T) {
	now := time.Now()
	update := asteraccount.LeverageUpdate{Symbol: "BTCUSDT", Leverage: 3}
	client := &fakeAsterAccountClient{private: &asterlive.PrivateResult{
		Operation:     asterlive.UpdateLeverage,
		AccountUpdate: &asteraccount.Update{Leverage: &update},
		SubmittedAt:   now, RespondedAt: now,
	}}
	feed := &asterAccountFeed{state: asteraccount.NewAccountState("0xowner"), client: client}
	request := &domain.SigningRequest{
		ID: "leverage", Venue: "aster", Action: string(asterlive.UpdateLeverage),
		Account: "0xowner", Symbol: "BTCUSDT", Leverage: 3,
	}
	result, err := feed.SubmitSigned(context.Background(), domain.SignedAction{}, request)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || !result.Accepted {
		t.Fatalf("submission result = %+v", result)
	}
	if err := feed.WaitForLeverage(context.Background(), "BTCUSDT", 3); err != nil {
		t.Fatal(err)
	}
}

func TestAsterAccountFeedResolvesNotionalLeverageCap(t *testing.T) {
	feed := &asterAccountFeed{state: asteraccount.NewAccountState("0xowner")}
	feed.state.ApplyLeverageBrackets(asteraccount.LeverageBrackets{"MEMEUSDT": {
		{InitialLeverage: 20, NotionalFloor: 0, NotionalCap: 1000},
		{InitialLeverage: 8, NotionalFloor: 1000, NotionalCap: 10000},
	}}, time.Now())

	maximum, found := feed.MaxLeverage("MEMEUSDT", 1500)
	if !found || maximum != 8 {
		t.Fatalf("maximum = %d, found = %v, want 8, true", maximum, found)
	}
}

func TestLiveDepsResolvesAsterAccountLeverageCap(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	account := "0x1111111111111111111111111111111111111111"
	registry := newAccountFeedRegistry(ctx, map[string]accountFeedFactory{
		"aster": &asterAccountFeedFactory{},
	}, accountFeedRegistryConfig{})
	lease, err := registry.Acquire("aster", account)
	if err != nil {
		t.Fatal(err)
	}
	lease.Release()
	live := &LiveDeps{accounts: registry}
	applied, err := live.applyAsterPrivateResult(context.Background(), &domain.SigningRequest{Account: account, CreatedAt: time.Now()}, &asterlive.PrivateResult{
		AccountUpdate: &asteraccount.Update{LeverageBrackets: asteraccount.LeverageBrackets{"MEMEUSDT": {
			{InitialLeverage: 12, NotionalFloor: 0, NotionalCap: 10000},
		}}},
	})
	if err != nil || !applied {
		t.Fatalf("applied = %v, error = %v", applied, err)
	}

	maximum, found := live.accountLeverageResolver(map[string]string{"aster": account})("aster", "MEMEUSDT", 500)
	if !found || maximum != 12 {
		t.Fatalf("maximum = %d, found = %v, want 12, true", maximum, found)
	}
}

func TestAsterAccountFeedAppliesDepositRequiredState(t *testing.T) {
	feed := &asterAccountFeed{state: asteraccount.NewAccountState("0xowner")}
	applied, err := feed.ApplyPrivateResult(&domain.SigningRequest{
		Account: "0xowner", Signer: "0xagent",
	}, &asterlive.PrivateResult{DepositRequired: true})
	if err != nil || !applied {
		t.Fatalf("applied = %v, error = %v", applied, err)
	}
	snapshot := feed.Snapshot()
	if snapshot.Connected || snapshot.UnavailableReason != "Aster account requires a deposit" {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}

func TestLiveDepsAppliesAsterDepositRequiredWithoutSnapshotUpdate(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	registry := newAccountFeedRegistry(ctx, map[string]accountFeedFactory{
		"aster": &asterAccountFeedFactory{},
	}, accountFeedRegistryConfig{})
	account := "0x1111111111111111111111111111111111111111"
	lease, err := registry.Acquire("aster", account)
	if err != nil {
		t.Fatal(err)
	}
	lease.Release()
	live := &LiveDeps{accounts: registry}

	applied, err := live.applyAsterPrivateResult(context.Background(), &domain.SigningRequest{
		Account: account, Signer: "0x2222222222222222222222222222222222222222",
	}, &asterlive.PrivateResult{DepositRequired: true})
	if err != nil || !applied {
		t.Fatalf("applied = %v, error = %v", applied, err)
	}
	current, found := registry.Lookup("aster", account)
	if !found {
		t.Fatal("Aster account feed not found")
	}
	snapshot := current.Feed().Snapshot()
	current.Release()
	if snapshot.UnavailableReason != "Aster account requires a deposit" {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}
