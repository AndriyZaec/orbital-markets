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

func TestAgentAuthorizationCannotMoveBetweenOwners(t *testing.T) {
	registry := newAgentAuthorizationRegistry()
	registry.record(
		"hyperliquid",
		"0x14791697260E4c9A71f18484C9f997B308e59325",
		"0x19E7E376E7C213B7E7e7e46cc70A5dD086DAff2A",
	)
	if !registry.matches(
		"hyperliquid",
		"0x14791697260e4c9a71f18484c9f997b308e59325",
		"0x19e7e376e7c213b7e7e7e46cc70a5dd086daff2a",
	) {
		t.Fatal("registered owner-agent pair was not found")
	}
	if registry.matches(
		"hyperliquid",
		"0x0000000000000000000000000000000000000001",
		"0x19e7e376e7c213b7e7e7e46cc70a5dd086daff2a",
	) {
		t.Fatal("agent authorization moved to another owner")
	}
}
