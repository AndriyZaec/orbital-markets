package orders

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/helper"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/rpc"
	"github.com/mr-tron/base58"
)

type Backend interface {
	CoSignOrder(context.Context, helper.BuildOrderResult, string, string, uint64) ([]byte, error)
	SimulateAndSend(context.Context, []byte, uint64, uint64) (string, error)
	SignatureStatus(context.Context, string) (*rpc.SignatureStatus, error)
}
type AccountingBackend interface {
	OrderAccounting(context.Context, *domain.SigningRequest, string) (ObservedAccounting, error)
}
type Admission struct {
	Before  Before
	MinSlot uint64
	Check   func(context.Context) error
}
type Validator func(context.Context, *domain.SigningRequest, Prepared) (Admission, error)
type StateProof func(context.Context, uint64) error

type Service struct {
	store    *Store
	backend  Backend
	history  *History
	requests [64]sync.Mutex
}

func NewService(store *Store, backend Backend, history *History) *Service {
	return &Service{store: store, backend: backend, history: history}
}

func rejected(q *domain.SigningRequest, reason string) *domain.SubmissionResult {
	return &domain.SubmissionResult{RequestID: q.ID, ClientOrderID: q.ClientOrderID, Venue: "phoenix", Error: reason, SubmittedAt: time.Now().UTC(), RespondedAt: time.Now().UTC()}
}
func outcome(a Attempt) (*domain.SubmissionResult, error) {
	r := &domain.SubmissionResult{RequestID: a.Request.ID, ClientOrderID: a.Request.ClientOrderID, Venue: "phoenix", OrderID: a.Signature,
		Accepted: a.State == "submitted" || a.State == "filled" || a.State == "no_fill", Error: a.Error, SubmittedAt: a.CreatedAt, RespondedAt: time.Now().UTC()}
	if a.State == "failed" {
		// The journal retains adapter evidence; public results must not expose it.
		r.Error = "Phoenix order failed"
		r.PrivateDiagnostic = a.Error
	}
	if a.State == "pending" {
		return r, ErrUncertain
	}
	return r, nil
}
func persistenceContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
}

func (s *Service) Submit(ctx context.Context, q *domain.SigningRequest, signed domain.SignedAction, validate Validator) (*domain.SubmissionResult, error) {
	if q == nil || validate == nil {
		return nil, ErrConflict
	}
	// One process owns the SQLite runtime. Serialize duplicate request calls
	// locally; distinct safety requests remain independent. SQLite Claim remains
	// the durable send fence across service/feed recreation and restart.
	key := sha256.Sum256([]byte(q.ID))
	lock := &s.requests[int(key[0])%len(s.requests)]
	lock.Lock()
	defer lock.Unlock()
	// No caller mutation can replace a message after asynchronous validation.
	digest, raw, err := requestDigest(q)
	if err != nil {
		return nil, err
	}
	var captured domain.SigningRequest
	if err := json.Unmarshal(raw, &captured); err != nil {
		return nil, err
	}
	q = &captured
	p, err := Decode(q)
	if err != nil {
		return rejected(q, "invalid stored Phoenix order"), nil
	}
	if err := verifySigned(q, p, signed); err != nil {
		return rejected(q, err.Error()), nil
	}
	prior, err := s.store.Get(ctx, q.Account, q.ID)
	if err == nil {
		if prior.Digest != digest {
			return nil, ErrConflict
		}
		return outcome(prior)
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	if !time.Now().Before(q.ExpiresAt) {
		return rejected(q, "Phoenix order signing request expired"), nil
	}
	ctx, cancel := context.WithDeadline(ctx, q.ExpiresAt)
	defer cancel()
	admission, err := validate(ctx, q, p)
	if err != nil {
		return rejected(q, err.Error()), nil
	}
	if admission.Check == nil || admission.MinSlot < p.Metadata.MinContextSlot || admission.MinSlot >= p.LastSlot || admission.Before.Slot == 0 {
		return rejected(q, "invalid Phoenix order admission evidence"), nil
	}
	packet, err := s.backend.CoSignOrder(ctx, p.Metadata.Built, q.Signer, signed.Signature, admission.MinSlot)
	if err != nil {
		return rejected(q, err.Error()), nil
	}
	message, _ := base64.StdEncoding.DecodeString(p.Payload.MessageBase64)
	payer, keyErr := base58.Decode(p.Payload.FeePayer)
	agentSignature, _ := base58.Decode(signed.Signature)
	if len(packet) != len(message)+129 || len(packet) > 1232 || packet[0] != 2 || !bytes.Equal(packet[129:], message) || !bytes.Equal(packet[65:129], agentSignature) || keyErr != nil || len(payer) != 32 || !ed25519.Verify(payer, message, packet[1:65]) {
		return rejected(q, "Phoenix relayer changed the stored transaction"), nil
	}
	if err := admission.Check(ctx); err != nil {
		return rejected(q, err.Error()), nil
	}
	if err := ctx.Err(); err != nil {
		return rejected(q, "Phoenix order expired or preparation cancelled"), nil
	}
	a := Attempt{Request: *q, Digest: digest, Signature: base58.Encode(packet[1:65]), Before: admission.Before, CreatedAt: time.Now().UTC(), State: "pending"}
	winner, err := s.store.Claim(ctx, a)
	if err != nil {
		return nil, err
	}
	if !winner {
		prior, err := s.store.Get(ctx, q.Account, q.ID)
		if err != nil || prior.Digest != digest {
			return nil, ErrConflict
		}
		return outcome(prior)
	}
	// A crash or lost response from here leaves durable pending evidence. This
	// signature is reconciled after reload/eviction; it is never sent a second time.
	if err := admission.Check(ctx); err != nil {
		persist, done := persistenceContext(ctx)
		defer done()
		if saveErr := s.store.Submitted(persist, a, "Phoenix admission changed before send"); saveErr != nil {
			return nil, ErrUncertain
		}
		return rejected(q, err.Error()), nil
	}
	signature, sendErr := s.backend.SimulateAndSend(ctx, packet, p.Metadata.Built.LastValidBlockHeight, admission.MinSlot)
	var ambiguous *rpc.AmbiguousSendError
	if errors.As(sendErr, &ambiguous) || (sendErr == nil && signature != a.Signature) {
		return &domain.SubmissionResult{RequestID: q.ID, ClientOrderID: q.ClientOrderID, Venue: "phoenix", OrderID: a.Signature, SubmittedAt: a.CreatedAt, RespondedAt: time.Now().UTC()}, ErrUncertain
	}
	failure := ""
	if sendErr != nil {
		failure = "Phoenix simulation/send rejected before broadcast: " + sendErr.Error()
		var diagnostic interface{ Diagnostic() string }
		if errors.As(sendErr, &diagnostic) {
			failure = "Phoenix simulation/send rejected before broadcast: " + diagnostic.Diagnostic()
		}
	}
	persist, done := persistenceContext(ctx)
	defer done()
	if err := s.store.Submitted(persist, a, failure); err != nil {
		return nil, ErrUncertain
	}
	stored, err := s.store.Get(persist, q.Account, q.ID)
	if err != nil {
		return nil, ErrUncertain
	}
	return outcome(stored)
}

func (s *Service) Reconcile(ctx context.Context, owner, id string, stateProof StateProof) (Attempt, error) {
	a, err := s.store.Get(ctx, owner, id)
	if err != nil {
		return a, err
	}
	if a.State == "filled" || a.State == "no_fill" || a.State == "failed" {
		return a, nil
	}
	status, err := s.backend.SignatureStatus(ctx, a.Signature)
	if err != nil {
		return a, err
	}
	if status == nil || (status.ConfirmationStatus != "confirmed" && status.ConfirmationStatus != "finalized") {
		return a, ErrUncertain
	}
	if status.Slot == 0 || len(status.Err) == 0 {
		return a, ErrUncertain
	}
	if !bytes.Equal(bytes.TrimSpace(status.Err), []byte("null")) {
		if err := s.store.Resolve(ctx, a, "failed", nil, "Phoenix transaction failed on chain"); err != nil {
			return a, err
		}
		return s.store.Get(ctx, owner, id)
	}
	if s.history == nil {
		return a, ErrUncertain
	}
	fill, err := s.history.Resolve(ctx, a, status.Slot)
	if err != nil {
		return a, err
	}
	if stateProof == nil {
		return a, ErrUncertain
	}
	minimum := status.Slot
	if fill.Status == "no_fill" {
		minimum = 0
	}
	if err := stateProof(ctx, minimum); err != nil {
		return a, err
	}
	state := "filled"
	if fill.Status == "no_fill" {
		state = "no_fill"
	}
	if err := s.store.Resolve(ctx, a, state, fill, ""); err != nil {
		return a, err
	}
	return s.store.Get(ctx, owner, id)
}

// Accounting is independent of exposure discovery: an unavailable transaction
// event read must not delay hedging or turn an actual fill into an unknown fill.
// Missing fee evidence remains explicitly incomplete and can be backfilled.
func (s *Service) Account(ctx context.Context, a Attempt) (Attempt, error) {
	b, ok := s.backend.(AccountingBackend)
	if !ok || a.Fill == nil || a.State != "filled" {
		return a, nil
	}
	if _, complete := a.Fill.TotalTradingFee(); complete {
		return a, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	observed, err := b.OrderAccounting(ctx, &a.Request, a.Signature)
	if err != nil {
		return a, err
	}
	p, err := Decode(&a.Request)
	if err != nil {
		return a, err
	}
	if observed.Slot != a.Fill.Slot || observed.Events.OwnerAuthority != a.Request.Account || observed.Events.TraderAccount != p.Metadata.Built.TraderAccount {
		return a, ErrConflict
	}
	network, parseErr := strconv.ParseUint(observed.NetworkFeeLamports, 10, 64)
	if parseErr != nil || strconv.FormatUint(network, 10) != observed.NetworkFeeLamports || observed.Events.DecodedEventCount == 0 {
		return a, ErrConflict
	}
	if value := observed.Events.FlightFeeQuoteLots; value != nil {
		fee, parseErr := strconv.ParseUint(*value, 10, 64)
		if parseErr != nil || strconv.FormatUint(fee, 10) != *value {
			return a, ErrConflict
		}
		if (p.Metadata.Built.Manifest.Routing == "native" && (fee != 0 || observed.Events.FlightTransferEvents != 0)) || (p.Metadata.Built.Manifest.Routing == "flight" && observed.Events.FlightTransferEvents == 0) {
			return a, ErrConflict
		}
	}
	if err := s.store.AttachAccounting(ctx, a, observed); err != nil {
		return a, err
	}
	return s.store.Get(ctx, a.Request.Account, a.Request.ID)
}

func (s *Service) Wait(ctx context.Context, owner, id string, stateProof StateProof) (Attempt, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		a, err := s.Reconcile(ctx, owner, id, stateProof)
		if err == nil {
			return a, nil
		}
		if errors.Is(err, ErrNotFound) || errors.Is(err, ErrConflict) {
			return a, err
		}
		select {
		case <-ctx.Done():
			return a, errors.Join(ErrUncertain, ctx.Err())
		case <-ticker.C:
		}
	}
}
