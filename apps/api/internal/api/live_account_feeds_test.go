package api

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	asteraccount "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/aster/account"
	asterlive "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/aster/live"
	pacaccount "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/pacifica/account"
)

type fakeAsterAccountClient struct {
	order   *domain.SubmissionResult
	private *asterlive.PrivateResult
	fill    *asterlive.FillResult
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
