package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	hlaccount "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/hyperliquid/account"
	hllive "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/hyperliquid/live"
	pacaccount "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/pacifica/account"
	paclive "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/pacifica/live"
)

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
	return &pacificaAccountFeed{state: state, tracker: tracker, client: client}, nil
}

type pacificaAccountFeed struct {
	state   *pacaccount.AccountState
	tracker *paclive.Tracker
	client  *paclive.Client
}

func (f *pacificaAccountFeed) Snapshot() liveAccountSnapshot {
	snapshot := f.state.Snapshot()
	positions := make([]liveAccountPosition, 0, len(snapshot.Positions))
	for _, position := range snapshot.Positions {
		positions = append(positions, liveAccountPosition{
			Symbol: position.Symbol, Side: position.Side,
			Size: position.Size, EntryPrice: position.EntryPrice,
		})
	}
	return liveAccountSnapshot{
		Venue: "pacifica", Account: snapshot.Account,
		Connected: snapshot.Connected, LastUpdated: snapshot.LastUpdated,
		PositionsUpdatedAt: snapshot.PositionsUpdatedAt,
		Equity:             snapshot.Equity, Available: snapshot.AvailableToSpend,
		Positions: positions,
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
	return f.client.SubmitSignedOrder(ctx, signed, request, f.tracker)
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
	return &hyperliquidAccountFeed{state: state, client: client}, nil
}

type hyperliquidAccountFeed struct {
	state  *hlaccount.AccountState
	client *hllive.Client
}

func (f *hyperliquidAccountFeed) Snapshot() liveAccountSnapshot {
	snapshot := f.state.Snapshot()
	positions := make([]liveAccountPosition, 0, len(snapshot.Positions))
	for _, position := range snapshot.Positions {
		positions = append(positions, liveAccountPosition{
			Symbol: position.Coin, Side: position.Side,
			Size: position.Size, EntryPrice: position.EntryPx,
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
