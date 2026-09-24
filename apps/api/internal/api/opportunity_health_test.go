package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/scanner"
)

func TestWritePlanErrorIncludesOpportunityHealth(t *testing.T) {
	recorder := httptest.NewRecorder()
	writePlanError(recorder, http.StatusUnprocessableEntity, &scanner.OpportunityStatusError{
		ID:     "PIPPIN-aster-pacifica",
		Status: domain.OpportunityDegraded,
		Reasons: []domain.OpportunityAvailabilityReason{{
			Code: domain.OpportunityReasonSourceFetchFailed, Venue: "aster",
		}},
	})

	var response struct {
		Status  domain.OpportunityStatus               `json:"opportunity_status"`
		Reasons []domain.OpportunityAvailabilityReason `json:"availability_reasons"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusUnprocessableEntity || response.Status != domain.OpportunityDegraded {
		t.Fatalf("response = %d %+v", recorder.Code, response)
	}
	if len(response.Reasons) != 1 || response.Reasons[0].Code != domain.OpportunityReasonSourceFetchFailed {
		t.Fatalf("availability reasons = %+v", response.Reasons)
	}
}
