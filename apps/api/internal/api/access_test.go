package api

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/scanner"
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

func TestSharedAsterLeverageUsesReferenceMarketMetadata(t *testing.T) {
	capability := publicLeverageCapability(marketLeverage{maximum: 20}, true, 500)

	if capability.Status != domain.LeverageCapabilityKnown || capability.Maximum == nil || *capability.Maximum != 20 {
		t.Fatalf("capability = %+v, want known 20x reference leverage", capability)
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
