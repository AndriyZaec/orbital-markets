package api

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/executor"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue"
	asteraccount "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/aster/account"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/aster/dataagent"
	asterlive "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/aster/live"
	hlaccount "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/hyperliquid/account"
	hllive "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/hyperliquid/live"
	pacaccount "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/pacifica/account"
	paclive "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/pacifica/live"
)

type asterAccountClient interface {
	SubmitSignedOrder(context.Context, domain.SignedAction, *domain.SigningRequest) (*domain.SubmissionResult, error)
	SubmitSignedPrivate(context.Context, domain.SignedAction, *domain.SigningRequest) (*asterlive.PrivateResult, error)
	WaitForFill(context.Context, string, string) (*asterlive.FillResult, error)
}

type asterAccountReader interface {
	ReadAccount(context.Context, string) (asteraccount.Observation, error)
}

type asterLeverageBracketReader interface {
	ReadLeverageBrackets(context.Context, string, string) (asteraccount.LeverageBrackets, time.Time, error)
}

type asterFundingReader interface {
	ReadFunding(context.Context, string, time.Time, time.Time) ([]venue.FundingPayment, error)
}

type asterOrderReader interface {
	LookupOrder(context.Context, string, string, string) (dataagent.OrderStatus, error)
}

type asterFundingApplier func(context.Context, string, []venue.FundingPayment, time.Time, time.Time) error

const (
	asterAccountPollInterval = 15 * time.Second
	asterAccountReadTimeout  = 10 * time.Second
	asterFundingPollInterval = time.Hour
	asterFundingReadTimeout  = 30 * time.Second
	asterFundingReadLookback = 7 * 24 * time.Hour
)

type asterAccountFeedFactory struct {
	client              asterAccountClient
	reader              asterAccountReader
	fundingReader       asterFundingReader
	orderReader         asterOrderReader
	applyFunding        asterFundingApplier
	logger              *slog.Logger
	pollInterval        time.Duration
	fundingPollInterval time.Duration
	backendReads        bool
}

func (f *asterAccountFeedFactory) Normalize(account string) (string, error) {
	account = strings.ToLower(strings.TrimSpace(account))
	if len(account) != 42 || !strings.HasPrefix(account, "0x") {
		return "", fmt.Errorf("invalid Aster account address")
	}
	if _, err := hex.DecodeString(account[2:]); err != nil {
		return "", fmt.Errorf("invalid Aster account address")
	}
	return account, nil
}

func (f *asterAccountFeedFactory) Start(ctx context.Context, account string) (liveAccountFeed, error) {
	feed := &asterAccountFeed{
		state: asteraccount.NewAccountState(account), client: f.client,
		account: account, reader: f.reader, logger: f.logger, ctx: ctx,
		fundingReader: f.fundingReader, orderReader: f.orderReader, applyFunding: f.applyFunding,
		backendReads: f.backendReads || f.reader != nil, now: time.Now,
	}
	if f.reader != nil {
		interval := f.pollInterval
		if interval <= 0 {
			interval = asterAccountPollInterval
		}
		go feed.runAccountPolling(interval)
	}
	if f.fundingReader != nil && f.applyFunding != nil {
		interval := f.fundingPollInterval
		if interval <= 0 {
			interval = asterFundingPollInterval
		}
		go feed.runFundingPolling(interval)
	}
	return feed, nil
}

type asterAccountFeed struct {
	state         *asteraccount.AccountState
	client        asterAccountClient
	account       string
	reader        asterAccountReader
	fundingReader asterFundingReader
	orderReader   asterOrderReader
	applyFunding  asterFundingApplier
	logger        *slog.Logger
	ctx           context.Context
	backendReads  bool
	now           func() time.Time

	refreshMu sync.Mutex
	refresh   *asterAccountRefresh
	lastRead  error

	fundingMu      sync.Mutex
	fundingRefresh *asterFundingRefresh

	bracketMu        sync.Mutex
	bracketRefreshes map[string]*asterBracketRefresh
}

type asterAccountRefresh struct {
	done chan struct{}
	err  error
}

type asterFundingRefresh struct {
	done chan struct{}
	err  error
}

type asterBracketRefresh struct {
	done chan struct{}
	err  error
}

func (f *asterAccountFeed) ApplyPrivateResult(request *domain.SigningRequest, result *asterlive.PrivateResult) (bool, error) {
	if result == nil {
		return false, nil
	}
	if f.backendReads {
		if result.AccountUpdate != nil && result.AccountUpdate.Leverage != nil {
			f.state.ApplyLeverage(*result.AccountUpdate.Leverage, result.RespondedAt)
		}
		return result.DepositRequired || result.AccountUpdate != nil, nil
	}
	if result.DepositRequired {
		return true, f.state.MarkUnavailable(request.Account, request.Signer, "Aster account requires a deposit")
	}
	if result.AccountUpdate == nil {
		return false, nil
	}
	update := result.AccountUpdate
	if update.SnapshotPart != nil {
		if request.SnapshotID == "" {
			return false, nil
		}
		apply := f.state.ApplySnapshotPart
		if asterlive.IsAccountRefreshSnapshot(request.SnapshotID) {
			apply = f.state.ApplyRefreshPart
		}
		if err := apply(request.Account, request.Signer, request.SnapshotID, request.CreatedAt, result.RespondedAt, *update.SnapshotPart); err != nil {
			return false, err
		}
	}
	if update.Leverage != nil {
		f.state.ApplyLeverage(*update.Leverage, result.RespondedAt)
	}
	if update.LeverageBrackets != nil {
		f.state.ApplyLeverageBrackets(update.LeverageBrackets, request.CreatedAt)
	}
	return true, nil
}

func (f *asterAccountFeed) Snapshot() liveAccountSnapshot {
	snapshot := f.state.Snapshot()
	f.refreshMu.Lock()
	lastRead := f.lastRead
	f.refreshMu.Unlock()
	unavailableReason := snapshot.UnavailableReason
	backendStale := snapshot.DataSource == asteraccount.AccountDataSourceBackend &&
		!snapshot.LastUpdated.IsZero() && time.Since(snapshot.LastUpdated) > admissionFreshness
	if !snapshot.Connected || snapshot.LastUpdated.IsZero() || backendStale {
		if unavailableReason == "" && f.backendReads && f.reader == nil {
			unavailableReason = "Aster backend account reader is not configured"
		} else if lastRead != nil && (snapshot.LastUpdated.IsZero() || (backendStale && asterReadAuthorizationFailed(lastRead))) {
			unavailableReason = asterAccountReadReason(lastRead)
		}
	}
	positions := make([]liveAccountPosition, 0, len(snapshot.Positions))
	for _, position := range snapshot.Positions {
		positions = append(positions, liveAccountPosition{
			Symbol: position.Symbol, Side: position.Side, Size: position.Size,
			EntryPrice: position.EntryPrice, LiqPrice: position.LiquidationPrice,
		})
	}
	return liveAccountSnapshot{
		Venue: "aster", Account: snapshot.Account, Connected: snapshot.Connected,
		LastUpdated: snapshot.LastUpdated, PositionsUpdatedAt: snapshot.PositionsUpdatedAt,
		Equity: snapshot.Equity, Available: snapshot.Available,
		Positions: positions, LeverageBySymbol: snapshot.LeverageBySymbol,
		UnavailableReason: unavailableReason,
	}
}

func (f *asterAccountFeed) PreTradeBlockers(leg domain.Leg) []string {
	return asteraccount.ValidatePreTrade(f.state.Snapshot(), leg.MarketKey, leg.MarginRequired, leg.Leverage)
}

func (f *asterAccountFeed) LeverageCapability(ctx context.Context, symbol string, notional float64, refresh bool) domain.LeverageCapability {
	capability := asteraccount.LeverageCapability(f.state.Snapshot(), symbol, notional, f.currentTime())
	if !refresh {
		return capability
	}
	accountNeedsRefresh := capability.Status == domain.LeverageCapabilityPending ||
		(capability.Status == domain.LeverageCapabilityStale && capability.Reason == domain.LeverageReasonAccountUnavailable)
	if accountNeedsRefresh && f.reader != nil && f.ctx != nil {
		accountRefresh := f.startAccountRefresh()
		select {
		case <-ctx.Done():
			capability.Reason = domain.LeverageReasonAccountUnavailable
			return capability
		case <-accountRefresh.done:
		}
		capability = asteraccount.LeverageCapability(f.state.Snapshot(), symbol, notional, f.currentTime())
	}
	bracketNeedsRefresh := capability.Status == domain.LeverageCapabilityMissing ||
		(capability.Status == domain.LeverageCapabilityStale && capability.Reason == domain.LeverageReasonBracketStale)
	if !bracketNeedsRefresh {
		return capability
	}
	refreshState := f.startLeverageBracketRefresh(symbol)
	select {
	case <-ctx.Done():
		capability.Reason = domain.LeverageReasonTargetRefreshFailed
		return capability
	case <-refreshState.done:
	}
	capability = asteraccount.LeverageCapability(f.state.Snapshot(), symbol, notional, f.currentTime())
	if refreshState.err != nil && capability.Status != domain.LeverageCapabilityKnown {
		capability.Reason = domain.LeverageReasonTargetRefreshFailed
	}
	return capability
}

func (f *asterAccountFeed) currentTime() time.Time {
	if f.now != nil {
		return f.now()
	}
	return time.Now()
}

func (f *asterAccountFeed) startLeverageBracketRefresh(symbol string) *asterBracketRefresh {
	f.bracketMu.Lock()
	if refresh := f.bracketRefreshes[symbol]; refresh != nil {
		f.bracketMu.Unlock()
		return refresh
	}
	refresh := &asterBracketRefresh{done: make(chan struct{})}
	if f.bracketRefreshes == nil {
		f.bracketRefreshes = make(map[string]*asterBracketRefresh)
	}
	f.bracketRefreshes[symbol] = refresh
	f.bracketMu.Unlock()

	go func() {
		reader, ok := f.reader.(asterLeverageBracketReader)
		if !ok {
			refresh.err = fmt.Errorf("Aster target leverage-bracket reader not configured")
		} else {
			baseCtx := f.ctx
			if baseCtx == nil {
				baseCtx = context.Background()
			}
			ctx, cancel := context.WithTimeout(baseCtx, asterAccountReadTimeout)
			brackets, observedAt, err := reader.ReadLeverageBrackets(ctx, f.account, symbol)
			cancel()
			refresh.err = err
			if err == nil {
				f.state.ApplyTargetLeverageBrackets(brackets, observedAt)
			}
		}
		f.bracketMu.Lock()
		delete(f.bracketRefreshes, symbol)
		close(refresh.done)
		f.bracketMu.Unlock()
	}()
	return refresh
}

func (f *asterAccountFeed) RefreshPositions(ctx context.Context) error {
	if f.reader == nil || f.ctx == nil {
		return fmt.Errorf("Aster backend account reader not configured")
	}
	refresh := f.startAccountRefresh()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-refresh.done:
		return refresh.err
	}
}

func (f *asterAccountFeed) startAccountRefresh() *asterAccountRefresh {
	f.refreshMu.Lock()
	if f.refresh != nil {
		refresh := f.refresh
		f.refreshMu.Unlock()
		return refresh
	}
	refresh := &asterAccountRefresh{done: make(chan struct{})}
	f.refresh = refresh
	f.refreshMu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(f.ctx, asterAccountReadTimeout)
		observation, err := f.reader.ReadAccount(ctx, f.account)
		cancel()
		if err == nil {
			err = f.state.ReplaceObservation(f.account, observation)
		}
		f.refreshMu.Lock()
		refresh.err = err
		f.lastRead = err
		if f.refresh == refresh {
			f.refresh = nil
		}
		close(refresh.done)
		f.refreshMu.Unlock()
	}()
	return refresh
}

func asterAccountReadReason(err error) string {
	switch {
	case errors.Is(err, dataagent.ErrNotApproved), errors.Is(err, dataagent.ErrRejected),
		errors.Is(err, dataagent.ErrUncertain), errors.Is(err, dataagent.ErrStale):
		return "Aster read authorization required"
	case errors.Is(err, dataagent.ErrUnavailable):
		return "Aster account reads unavailable"
	case errors.Is(err, dataagent.ErrCredentialUnreadable):
		return dataagent.ErrCredentialUnreadable.Error()
	case errors.Is(err, dataagent.ErrReadRejected):
		return "Aster account reads were rejected; reauthorize Aster"
	default:
		return "Aster account refresh failed"
	}
}

func asterReadAuthorizationFailed(err error) bool {
	return errors.Is(err, dataagent.ErrNotApproved) || errors.Is(err, dataagent.ErrRejected) ||
		errors.Is(err, dataagent.ErrUncertain) || errors.Is(err, dataagent.ErrStale) ||
		errors.Is(err, dataagent.ErrCredentialUnreadable) || errors.Is(err, dataagent.ErrReadRejected)
}

func (f *asterAccountFeed) runAccountPolling(interval time.Duration) {
	refresh := func() {
		if err := f.RefreshPositions(f.ctx); err != nil && f.ctx.Err() == nil && f.logger != nil {
			f.logger.Warn("aster: account refresh failed", "err", err)
		}
	}
	refresh()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-f.ctx.Done():
			return
		case <-ticker.C:
			refresh()
		}
	}
}

func (f *asterAccountFeed) RefreshFunding(ctx context.Context) error {
	if f.fundingReader == nil || f.applyFunding == nil || f.ctx == nil {
		return fmt.Errorf("Aster backend funding reader not configured")
	}
	refresh := f.startFundingRefresh()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-refresh.done:
		return refresh.err
	}
}

func (f *asterAccountFeed) startFundingRefresh() *asterFundingRefresh {
	f.fundingMu.Lock()
	if f.fundingRefresh != nil {
		refresh := f.fundingRefresh
		f.fundingMu.Unlock()
		return refresh
	}
	refresh := &asterFundingRefresh{done: make(chan struct{})}
	f.fundingRefresh = refresh
	f.fundingMu.Unlock()

	go func() {
		submittedAt := time.Now().UTC()
		ctx, cancel := context.WithTimeout(f.ctx, asterFundingReadTimeout)
		payments, err := f.fundingReader.ReadFunding(ctx, f.account, submittedAt.Add(-asterFundingReadLookback), submittedAt)
		respondedAt := time.Now().UTC()
		if err == nil {
			err = f.applyFunding(ctx, f.account, payments, submittedAt, respondedAt)
		}
		cancel()
		f.fundingMu.Lock()
		refresh.err = err
		if f.fundingRefresh == refresh {
			f.fundingRefresh = nil
		}
		close(refresh.done)
		f.fundingMu.Unlock()
	}()
	return refresh
}

func (f *asterAccountFeed) runFundingPolling(interval time.Duration) {
	refresh := func() {
		if err := f.RefreshFunding(f.ctx); err != nil && f.ctx.Err() == nil && f.logger != nil {
			f.logger.Warn("aster: funding refresh failed", "err", err)
		}
	}
	refresh()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-f.ctx.Done():
			return
		case <-ticker.C:
			refresh()
		}
	}
}

func (f *asterAccountFeed) SubmitSigned(
	ctx context.Context,
	signed domain.SignedAction,
	request *domain.SigningRequest,
) (*domain.SubmissionResult, error) {
	if f.client == nil {
		return nil, fmt.Errorf("Aster live client not configured")
	}
	if request.Action != string(asterlive.UpdateLeverage) {
		return f.client.SubmitSignedOrder(ctx, signed, request)
	}
	result, err := f.client.SubmitSignedPrivate(ctx, signed, request)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, fmt.Errorf("Aster leverage response missing")
	}
	applied, err := f.ApplyPrivateResult(request, result)
	if err != nil {
		return nil, err
	}
	if !applied {
		return nil, fmt.Errorf("Aster leverage response did not update account state")
	}
	return &domain.SubmissionResult{
		RequestID: request.ID, ClientOrderID: request.ClientOrderID,
		Venue: "aster", Accepted: true,
		SubmittedAt: result.SubmittedAt, RespondedAt: result.RespondedAt,
	}, nil
}

func (f *asterAccountFeed) WaitForFill(ctx context.Context, request *domain.SigningRequest) (*normFill, error) {
	if request == nil {
		return nil, fmt.Errorf("Aster fill request missing")
	}
	if f.client != nil {
		fill, err := f.client.WaitForFill(ctx, request.Account, request.ClientOrderID)
		if err == nil && fill != nil {
			normalized := &normFill{
				FilledAmount: fill.FilledAmount, AvgFillPrice: fill.AvgFillPrice,
				OrderID: fill.OrderID, Status: fill.Status, Filled: fill.Filled,
			}
			if f.orderReader != nil && fill.FilledAmount > 0 {
				order, lookupErr := f.orderReader.LookupOrder(ctx, request.Account, request.Symbol, request.ClientOrderID)
				exact, normalizeErr := normFillFromAsterOrder(order, request)
				if lookupErr == nil && normalizeErr == nil && exact.OrderID == fill.OrderID &&
					math.Abs(exact.FilledAmount-fill.FilledAmount) <= math.Max(1e-12, fill.FilledAmount*1e-9) &&
					math.Abs(exact.AvgFillPrice-fill.AvgFillPrice) <= math.Max(1e-12, fill.AvgFillPrice*1e-9) {
					normalized.Fee = exact.Fee
				}
			}
			return normalized, nil
		}
		if f.orderReader == nil {
			if err != nil {
				return nil, err
			}
			return nil, fmt.Errorf("Aster fill result unavailable for %s", request.ClientOrderID)
		}
	} else if f.orderReader == nil {
		return nil, fmt.Errorf("Aster fill tracking not configured")
	}
	order, err := f.orderReader.LookupOrder(ctx, request.Account, request.Symbol, request.ClientOrderID)
	if err != nil {
		return nil, fmt.Errorf("Aster exact-order lookup failed: %w", err)
	}
	return normFillFromAsterOrder(order, request)
}

func normFillFromAsterOrder(order dataagent.OrderStatus, request *domain.SigningRequest) (*normFill, error) {
	if order.ClientOrderID != request.ClientOrderID || order.Symbol != request.Symbol ||
		math.IsNaN(order.ExecutedQuantity) || math.IsInf(order.ExecutedQuantity, 0) || order.ExecutedQuantity < 0 ||
		order.ExecutedQuantity > request.Amount || math.IsNaN(order.AveragePrice) ||
		math.IsInf(order.AveragePrice, 0) || order.AveragePrice < 0 ||
		order.ExecutedQuantity > 0 && order.AveragePrice == 0 {
		return nil, fmt.Errorf("Aster exact-order response correlation mismatch")
	}
	status := strings.ToUpper(order.Status)
	switch status {
	case "FILLED":
		if order.ExecutedQuantity != request.Amount || order.ExecutedQuantity == 0 {
			return nil, fmt.Errorf("Aster exact-order filled quantity mismatch")
		}
	case "CANCELED", "EXPIRED":
	case "REJECTED":
		if order.ExecutedQuantity != 0 {
			return nil, fmt.Errorf("Aster rejected order reports an execution")
		}
	case "NEW", "PARTIALLY_FILLED":
		return nil, fmt.Errorf("Aster exact order is not terminal: %s", status)
	default:
		return nil, fmt.Errorf("Aster exact order has invalid status")
	}
	normalizedStatus := strings.ToLower(status)
	if order.ExecutedQuantity > 0 && status != "FILLED" {
		normalizedStatus = "partial_fill"
	}
	return &normFill{
		FilledAmount: order.ExecutedQuantity, AvgFillPrice: order.AveragePrice,
		Fee: order.Fee, OrderID: order.OrderID, Status: normalizedStatus, Filled: order.ExecutedQuantity > 0,
	}, nil
}

func (f *asterAccountFeed) WaitForLeverage(_ context.Context, symbol string, leverage float64) error {
	if math.Abs(f.state.Snapshot().LeverageBySymbol[symbol]-leverage) > 1e-9 {
		return fmt.Errorf("Aster leverage was not confirmed")
	}
	return nil
}

type pacificaAccountFeedFactory struct {
	logger *slog.Logger
}

func (f *pacificaAccountFeedFactory) Normalize(account string) (string, error) {
	account = strings.TrimSpace(account)
	if account == "" {
		return "", fmt.Errorf("pacifica account required")
	}
	return account, nil
}

func (f *pacificaAccountFeedFactory) Start(ctx context.Context, account string) (liveAccountFeed, error) {
	state := pacaccount.NewAccountState()
	state.ResetForAccount(account)
	tracker := paclive.NewTracker(f.logger)
	client := paclive.NewClient(f.logger, nil, state)
	subscriber := pacaccount.NewSubscriber(f.logger, state, account, tracker)
	go subscriber.Run(ctx)
	return &pacificaAccountFeed{state: state, tracker: tracker, client: client, subscriber: subscriber}, nil
}

type pacificaAccountFeed struct {
	state      *pacaccount.AccountState
	tracker    *paclive.Tracker
	client     *paclive.Client
	subscriber *pacaccount.Subscriber
}

func (f *pacificaAccountFeed) RefreshPositions(ctx context.Context) error {
	return f.subscriber.RefreshPositions(ctx)
}

func (f *pacificaAccountFeed) Snapshot() liveAccountSnapshot {
	snapshot := f.state.Snapshot()
	leverages := make(map[string]float64, len(snapshot.SymbolConfigs))
	for symbol, config := range snapshot.SymbolConfigs {
		leverages[symbol] = config.Leverage
	}
	positions := make([]liveAccountPosition, 0, len(snapshot.Positions))
	for _, position := range snapshot.Positions {
		positions = append(positions, liveAccountPosition{
			Symbol: position.Symbol, Side: position.Side,
			Size: position.Size, EntryPrice: position.EntryPrice, LiqPrice: position.LiqPrice,
		})
	}
	return liveAccountSnapshot{
		Venue: "pacifica", Account: snapshot.Account,
		Connected: snapshot.Connected, LastUpdated: snapshot.LastUpdated,
		PositionsUpdatedAt: snapshot.PositionsUpdatedAt,
		Equity:             snapshot.Equity, Available: snapshot.AvailableToSpend,
		Positions: positions, LeverageBySymbol: leverages,
	}
}

func (f *pacificaAccountFeed) WaitForLeverage(ctx context.Context, symbol string, leverage float64) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if current := f.state.Snapshot().SymbolConfigs[symbol].Leverage; math.Abs(current-leverage) < 1e-9 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (f *pacificaAccountFeed) PreTradeBlockers(leg domain.Leg) []string {
	result := pacaccount.ValidatePreTrade(f.state.Snapshot(), leg.Asset, leg.MarginRequired, leg.Leverage)
	if result.CanProceed() {
		return nil
	}
	return result.Reasons
}

func (f *pacificaAccountFeed) SubmitSigned(
	ctx context.Context,
	signed domain.SignedAction,
	request *domain.SigningRequest,
) (*domain.SubmissionResult, error) {
	if request.Action == "update_leverage" {
		result, err := f.client.SubmitSignedLeverage(ctx, signed, request)
		if err == nil && result != nil && result.Accepted {
			applyAcceptedPacificaLeverage(f.state, request)
		}
		return result, err
	}
	return f.client.SubmitSignedOrder(ctx, signed, request, f.tracker)
}

func applyAcceptedPacificaLeverage(state *pacaccount.AccountState, request *domain.SigningRequest) {
	config := state.Snapshot().SymbolConfigs[request.Symbol]
	config.Symbol = request.Symbol
	config.Leverage = float64(request.Leverage)
	state.UpdateSymbolConfigForAccount(request.Account, config)
}

func (f *pacificaAccountFeed) WaitForFill(ctx context.Context, request *domain.SigningRequest) (*normFill, error) {
	fill, err := f.tracker.WaitForFill(ctx, request.ClientOrderID)
	if err != nil {
		return nil, err
	}
	return &normFill{
		FilledAmount: fill.FilledAmount, AvgFillPrice: fill.AvgFillPrice, Fee: fill.TotalFee,
		OrderID: fill.OrderID, Status: string(fill.Status),
		Filled: fill.Status == paclive.OrderStatusFilled || fill.Status == paclive.OrderStatusPartialFill,
	}, nil
}

type hyperliquidAccountFeedFactory struct {
	logger   *slog.Logger
	assetMap hllive.AssetMap
}

func (f *hyperliquidAccountFeedFactory) Normalize(account string) (string, error) {
	account = strings.ToLower(strings.TrimSpace(account))
	if account == "" {
		return "", fmt.Errorf("hyperliquid account required")
	}
	return account, nil
}

func (f *hyperliquidAccountFeedFactory) Start(ctx context.Context, account string) (liveAccountFeed, error) {
	state := hlaccount.NewAccountState()
	state.ResetForAccount(account)
	tracker := hllive.NewTracker(f.logger, account)
	client := hllive.NewClient(f.logger, nil, f.assetMap, state, tracker)
	subscriber := hlaccount.NewSubscriber(f.logger, state, account)
	go subscriber.Run(ctx)
	go tracker.Run(ctx)
	return &hyperliquidAccountFeed{state: state, client: client, subscriber: subscriber}, nil
}

type hyperliquidAccountFeed struct {
	state      *hlaccount.AccountState
	client     *hllive.Client
	subscriber *hlaccount.Subscriber
}

func (f *hyperliquidAccountFeed) RefreshPositions(ctx context.Context) error {
	return f.subscriber.RefreshPositions(ctx)
}

func (f *hyperliquidAccountFeed) Snapshot() liveAccountSnapshot {
	snapshot := f.state.Snapshot()
	positions := make([]liveAccountPosition, 0, len(snapshot.Positions))
	for _, position := range snapshot.Positions {
		positions = append(positions, liveAccountPosition{
			Symbol: position.Coin, Side: position.Side,
			Size: position.Size, EntryPrice: position.EntryPx, LiqPrice: position.LiquidationPx,
		})
	}
	return liveAccountSnapshot{
		Venue: "hyperliquid", Account: snapshot.Account,
		Connected: snapshot.Connected, LastUpdated: snapshot.LastUpdated,
		PositionsUpdatedAt: snapshot.PositionsUpdatedAt,
		Equity:             snapshot.Margin.AccountEquity, Available: snapshot.Margin.AvailableBalance,
		Positions: positions,
	}
}

func (d *LiveDeps) LiquidationPrices(_ context.Context, position *executor.LivePosition) (map[string]float64, error) {
	accounts, err := d.acquireAccountContext(position.AccountBindings, true)
	if err != nil {
		return nil, err
	}
	defer accounts.Release()
	prices := make(map[string]float64, 2)
	for _, venue := range []string{position.VenueA, position.VenueB} {
		feed, ok := accounts.Feed(venue)
		if !ok {
			return nil, fmt.Errorf("%s account feed unavailable", venue)
		}
		snapshot := feed.Snapshot()
		if snapshot.PositionsUpdatedAt.IsZero() || time.Since(snapshot.PositionsUpdatedAt) > admissionFreshness {
			continue
		}
		for _, accountPosition := range snapshot.Positions {
			if strings.EqualFold(accountPosition.Symbol, position.Asset) && accountPosition.LiqPrice > 0 {
				prices[venue] = accountPosition.LiqPrice
				break
			}
		}
	}
	return prices, nil
}

func (f *hyperliquidAccountFeed) PreTradeBlockers(leg domain.Leg) []string {
	result := hlaccount.ValidatePreTrade(f.state.Snapshot(), leg.Asset, leg.MarginRequired, leg.Leverage)
	if result.CanProceed() {
		return nil
	}
	return result.Reasons
}

func (f *hyperliquidAccountFeed) SubmitSigned(
	ctx context.Context,
	signed domain.SignedAction,
	request *domain.SigningRequest,
) (*domain.SubmissionResult, error) {
	if request.Action == "update_leverage" {
		return f.client.SubmitSignedLeverage(ctx, signed, request)
	}
	return f.client.SubmitSignedOrder(ctx, signed, request)
}

func (f *hyperliquidAccountFeed) WaitForFill(ctx context.Context, request *domain.SigningRequest) (*normFill, error) {
	var metadata struct {
		Cloid string `json:"cloid"`
	}
	if err := json.Unmarshal(request.VenueMetadata, &metadata); err != nil {
		return nil, fmt.Errorf("parse hyperliquid venue metadata: %w", err)
	}
	fill, err := f.client.WaitForFill(ctx, metadata.Cloid)
	if err != nil {
		return nil, err
	}
	return &normFill{
		FilledAmount: fill.FilledAmount, AvgFillPrice: fill.AvgFillPrice, Fee: fill.TotalFee,
		OrderID: fill.OrderID, Status: string(fill.Status),
		Filled: fill.Status == hllive.OrderStatusFilled || fill.Status == hllive.OrderStatusPartialFill,
	}, nil
}

func (f *hyperliquidAccountFeed) WaitForLeverage(context.Context, string, float64) error {
	return nil
}
