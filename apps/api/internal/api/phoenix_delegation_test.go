package api

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/delegation"
)

type fakePhoenixDelegation struct {
	calls int
	state string
	err   error
}

func (f *fakePhoenixDelegation) Status(context.Context, string, string) (delegation.AuthorityStatus, error) {
	return delegation.AuthorityStatus{}, nil
}

func (f *fakePhoenixDelegation) Prepare(context.Context, delegation.PrepareRequest) (delegation.Result, error) {
	f.calls++
	return delegation.Result{State: f.state}, f.err
}
func (f *fakePhoenixDelegation) Submit(context.Context, string, domain.SignedAction) (delegation.Result, error) {
	f.calls++
	return delegation.Result{State: f.state}, f.err
}
func (f *fakePhoenixDelegation) Reconcile(context.Context, string, string) (delegation.Result, error) {
	f.calls++
	return delegation.Result{State: f.state}, f.err
}

func TestPhoenixDelegationHTTPStatesAndStrictInput(t *testing.T) {
	for _, tc := range []struct {
		state string
		err   error
		want  int
	}{
		{"prepared", nil, 200}, {"pending", nil, 202}, {"confirmed", nil, 200}, {"expired", nil, 409}, {"failed", nil, 422},
		{"", delegation.ErrReplacementRequired, 409}, {"", delegation.ErrInvalid, 400}, {"", errors.New("RPC unavailable"), 503},
	} {
		fake := &fakePhoenixDelegation{state: tc.state, err: tc.err}
		s := &Server{}
		s.EnablePhoenixDelegation(fake)
		w := httptest.NewRecorder()
		s.handlePhoenixDelegationPrepare(w, httptest.NewRequest("POST", "/", strings.NewReader(`{"owner":"owner","agent":"agent","action":"delegate"}`)))
		if w.Code != tc.want || fake.calls != 1 {
			t.Fatalf("state=%s code=%d calls=%d", tc.state, w.Code, fake.calls)
		}
	}
	fake := &fakePhoenixDelegation{}
	s := &Server{}
	s.EnablePhoenixDelegation(fake)
	w := httptest.NewRecorder()
	s.handlePhoenixDelegationSubmit(w, httptest.NewRequest("POST", "/", strings.NewReader(`{"owner":"owner","signed_action":{},"message_base64":"untrusted"}`)))
	if w.Code != 400 || fake.calls != 0 {
		t.Fatal("browser-supplied message accepted")
	}
	s = &Server{}
	w = httptest.NewRecorder()
	s.handlePhoenixDelegationReconcile(w, httptest.NewRequest("POST", "/", strings.NewReader(`{}`)))
	if w.Code != 503 {
		t.Fatal("disabled delegation did not fail closed")
	}
}
