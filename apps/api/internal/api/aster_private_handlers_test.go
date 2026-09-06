package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	asterlive "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/aster/live"
)

type fakeAsterPrivateSubmitter struct {
	request *domain.SigningRequest
	err     error
}

func (f *fakeAsterPrivateSubmitter) SubmitSignedPrivate(
	_ context.Context,
	_ domain.SignedAction,
	request *domain.SigningRequest,
) (*asterlive.PrivateResult, error) {
	f.request = request
	if f.err != nil {
		return nil, f.err
	}
	return &asterlive.PrivateResult{
		Operation: asterlive.GetPositionMode, Data: json.RawMessage(`{"dualSidePosition":false}`),
		SubmittedAt: time.Now(), RespondedAt: time.Now(),
	}, nil
}

func TestAsterPrivateSubmitRestoresConfirmedNotSentRequest(t *testing.T) {
	submitter := &fakeAsterPrivateSubmitter{err: fmt.Errorf("%w: invalid signature", asterlive.ErrSubmissionNotSent)}
	server, signedBody := preparedAsterPrivateRequest(t, submitter)

	first := httptest.NewRecorder()
	server.handleAsterPrivateSubmit(first, httptest.NewRequest(
		http.MethodPost, "/api/v1/live/aster/private/submit", bytes.NewReader(signedBody),
	))
	if first.Code != http.StatusBadRequest {
		t.Fatalf("first status = %d, body = %s", first.Code, first.Body.String())
	}
	submitter.err = nil
	second := httptest.NewRecorder()
	server.handleAsterPrivateSubmit(second, httptest.NewRequest(
		http.MethodPost, "/api/v1/live/aster/private/submit", bytes.NewReader(signedBody),
	))
	if second.Code != http.StatusOK {
		t.Fatalf("second status = %d, body = %s", second.Code, second.Body.String())
	}
}

func TestAsterPrivateSubmitConsumesAmbiguousRequest(t *testing.T) {
	submitter := &fakeAsterPrivateSubmitter{err: fmt.Errorf("%w: HTTP 503", asterlive.ErrSubmissionAmbiguous)}
	server, signedBody := preparedAsterPrivateRequest(t, submitter)

	first := httptest.NewRecorder()
	server.handleAsterPrivateSubmit(first, httptest.NewRequest(
		http.MethodPost, "/api/v1/live/aster/private/submit", bytes.NewReader(signedBody),
	))
	if first.Code != http.StatusAccepted {
		t.Fatalf("first status = %d, body = %s", first.Code, first.Body.String())
	}
	submitter.err = nil
	second := httptest.NewRecorder()
	server.handleAsterPrivateSubmit(second, httptest.NewRequest(
		http.MethodPost, "/api/v1/live/aster/private/submit", bytes.NewReader(signedBody),
	))
	if second.Code != http.StatusBadRequest {
		t.Fatalf("second status = %d, body = %s", second.Code, second.Body.String())
	}
}

func TestLiveSubmitDoesNotConsumeAsterPrivateRequest(t *testing.T) {
	submitter := &fakeAsterPrivateSubmitter{}
	server, signedBody := preparedAsterPrivateRequest(t, submitter)
	server.logger = slog.New(slog.NewTextHandler(io.Discard, nil))

	wrongEndpoint := httptest.NewRecorder()
	server.handleLiveSubmit(wrongEndpoint, httptest.NewRequest(
		http.MethodPost, "/api/v1/live/submit", bytes.NewReader(signedBody),
	))
	if wrongEndpoint.Code != http.StatusForbidden {
		t.Fatalf("wrong endpoint status = %d, body = %s", wrongEndpoint.Code, wrongEndpoint.Body.String())
	}
	rightEndpoint := httptest.NewRecorder()
	server.handleAsterPrivateSubmit(rightEndpoint, httptest.NewRequest(
		http.MethodPost, "/api/v1/live/aster/private/submit", bytes.NewReader(signedBody),
	))
	if rightEndpoint.Code != http.StatusOK {
		t.Fatalf("right endpoint status = %d, body = %s", rightEndpoint.Code, rightEndpoint.Body.String())
	}
}

func preparedAsterPrivateRequest(t *testing.T, submitter *fakeAsterPrivateSubmitter) (*Server, []byte) {
	t.Helper()
	server := &Server{live: &LiveDeps{
		signingStore: domain.NewSigningRequestStore(), asterPrivate: submitter,
	}}
	prepareResponse := httptest.NewRecorder()
	server.handleAsterPrivatePrepare(prepareResponse, httptest.NewRequest(
		http.MethodPost, "/api/v1/live/aster/private/prepare",
		strings.NewReader(`{"operation":"get_position_mode","account":"0x1111111111111111111111111111111111111111","agent":"0x2222222222222222222222222222222222222222"}`),
	))
	if prepareResponse.Code != http.StatusOK {
		t.Fatalf("prepare status = %d, body = %s", prepareResponse.Code, prepareResponse.Body.String())
	}
	var request domain.SigningRequest
	if err := json.Unmarshal(prepareResponse.Body.Bytes(), &request); err != nil {
		t.Fatal(err)
	}
	signedBody, err := json.Marshal(domain.SignedAction{
		RequestID: request.ID, ClientOrderID: request.ClientOrderID, Venue: "aster",
		SignerAddress: request.Signer, Signature: "0x" + strings.Repeat("1", 128) + "1b",
	})
	if err != nil {
		t.Fatal(err)
	}
	return server, signedBody
}

func TestAsterPrivatePrepareAndSubmitUsesStoredAllowlistedRequest(t *testing.T) {
	submitter := &fakeAsterPrivateSubmitter{}
	server := &Server{live: &LiveDeps{
		signingStore: domain.NewSigningRequestStore(), asterPrivate: submitter,
	}}
	prepareBody := `{"operation":"get_position_mode","account":"0x1111111111111111111111111111111111111111","agent":"0x2222222222222222222222222222222222222222"}`
	prepareResponse := httptest.NewRecorder()
	server.handleAsterPrivatePrepare(prepareResponse, httptest.NewRequest(
		http.MethodPost, "/api/v1/live/aster/private/prepare", strings.NewReader(prepareBody),
	))
	if prepareResponse.Code != http.StatusOK {
		t.Fatalf("prepare status = %d, body = %s", prepareResponse.Code, prepareResponse.Body.String())
	}
	var request domain.SigningRequest
	if err := json.Unmarshal(prepareResponse.Body.Bytes(), &request); err != nil {
		t.Fatal(err)
	}
	signed := domain.SignedAction{
		RequestID: request.ID, ClientOrderID: request.ClientOrderID, Venue: "aster",
		SignerAddress: request.Signer, Signature: "0x" + strings.Repeat("1", 128) + "1b",
	}
	signedBody, _ := json.Marshal(signed)
	submitResponse := httptest.NewRecorder()
	server.handleAsterPrivateSubmit(submitResponse, httptest.NewRequest(
		http.MethodPost, "/api/v1/live/aster/private/submit", bytes.NewReader(signedBody),
	))
	if submitResponse.Code != http.StatusOK || submitter.request == nil || submitter.request.Action != "get_position_mode" {
		t.Fatalf("submit status = %d, body = %s, request = %+v", submitResponse.Code, submitResponse.Body.String(), submitter.request)
	}
}

func TestAsterPrivatePrepareRejectsUnknownOperation(t *testing.T) {
	server := &Server{live: &LiveDeps{
		signingStore: domain.NewSigningRequestStore(), asterPrivate: &fakeAsterPrivateSubmitter{},
	}}
	response := httptest.NewRecorder()
	server.handleAsterPrivatePrepare(response, httptest.NewRequest(
		http.MethodPost, "/api/v1/live/aster/private/prepare",
		strings.NewReader(`{"operation":"withdraw","account":"0x1111111111111111111111111111111111111111","agent":"0x2222222222222222222222222222222222222222"}`),
	))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}
