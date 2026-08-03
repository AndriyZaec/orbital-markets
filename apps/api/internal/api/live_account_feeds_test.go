package api

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
)

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
		Venue: "hyperliquid", Account: "0xAbCd",
	}, "0xabcd"); err != nil {
		t.Fatalf("case-insensitive Hyperliquid signer rejected: %v", err)
	}
	if err := live.validateSigningAccount(&domain.SigningRequest{
		Venue: "pacifica", Account: "SolCaseSensitive",
	}, "solcasesensitive"); err == nil {
		t.Fatal("case-mismatched Pacifica signer accepted")
	}
}
