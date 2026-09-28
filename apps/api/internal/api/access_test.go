package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/scanner"
	asteraccount "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/aster/account"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/aster/bracketcache"
)

func TestAccessProbeReturnsNoContent(t *testing.T) {
	response := httptest.NewRecorder()
	(&Server{}).handleAccess(response, httptest.NewRequest(http.MethodGet, "/api/v1/access", nil))

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNoContent)
	}
	if response.Body.Len() != 0 {
		t.Fatalf("body = %q, want empty", response.Body.String())
	}
}

func TestSharedNonAsterLeverageUsesPublicMarketMetadata(t *testing.T) {
	capability := publicLeverageCapability("hyperliquid", marketLeverage{maximum: 20}, true, 500, bracketcache.Snapshot{})

	if capability.Status != domain.LeverageCapabilityKnown || capability.Maximum == nil || *capability.Maximum != 20 {
		t.Fatalf("capability = %+v, want known 20x reference leverage", capability)
	}
}

func TestSharedAsterLeverageWithoutReferenceSnapshotIsUnsupported(t *testing.T) {
	capability := publicLeverageCapability("aster", marketLeverage{maximum: 20}, true, 500, bracketcache.Snapshot{})

	if capability.Status != domain.LeverageCapabilityUnsupported || capability.Reason != domain.LeverageReasonReferenceUnavailable {
		t.Fatalf("capability = %+v, want unsupported reference leverage", capability)
	}
}

func TestSharedAsterLeverageUsesReferenceBracketTier(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	cache := bracketcache.New(bracketcache.ReaderFunc(func(context.Context) (asteraccount.LeverageBrackets, time.Time, error) {
		return asteraccount.LeverageBrackets{"PIPPINUSDT": {{InitialLeverage: 8, NotionalFloor: 1_000, NotionalCap: 10_000}}}, now, nil
	}), slog.New(slog.NewTextHandler(io.Discard, nil)), func() time.Time { return now })
	if err := cache.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}

	capability := publicLeverageCapability(
		"aster", marketLeverage{marketKey: "PIPPINUSDT"}, true, 1_500, cache.Snapshot(),
	)
	if capability.Status != domain.LeverageCapabilityKnown || capability.Maximum == nil || *capability.Maximum != 8 || capability.BracketRevision != 1 {
		t.Fatalf("capability = %+v, want known 8x reference leverage", capability)
	}
}

func TestOpportunitiesIgnoreAccountQueryParameters(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := &Server{scanner: scanner.New(logger), logger: logger}
	response := httptest.NewRecorder()
	server.handleOpportunities(response, httptest.NewRequest(
		http.MethodGet,
		"/api/v1/opportunities?accounts%5Baster%5D=not-an-account",
		nil,
	))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusOK, response.Body.String())
	}
}
