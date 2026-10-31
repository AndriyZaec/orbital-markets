// Package delegation coordinates owner-approved Phoenix authority changes.
// RPC acceptance is not confirmation; durable pending sends reconcile on chain.
package delegation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/helper"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/rpc"
	"github.com/google/uuid"
	"github.com/mr-tron/base58"
)

var (
	ErrInvalid             = errors.New("invalid Phoenix delegation request")
	ErrReplacementRequired = errors.New("confirm replacement of existing Phoenix delegate")
	ErrAuthorityChanged    = errors.New("Phoenix position authority changed; prepare again")
)

type Authority struct {
	PositionAuthority string
	Slot              uint64
}

type Backend interface {
	Address() string
	LatestBlockhash(context.Context) (rpc.Blockhash, error)
	BuildDelegation(context.Context, helper.BuildDelegationRequest) (helper.BuildDelegationResult, error)
	ReadAuthority(context.Context, string, string, uint64, bool) (Authority, error)
	CoSignDelegation(context.Context, helper.BuildDelegationResult, string, string, uint64) ([]byte, error)
	SimulateAndSend(context.Context, []byte, uint64, uint64) (string, error)
	SignatureStatus(context.Context, string) (*rpc.SignatureStatus, error)
	FinalizedBlockhashValidity(context.Context, string, uint64) (rpc.BlockhashValidity, error)
}

type Service struct {
	store   *Store
	backend Backend
}

func NewService(store *Store, backend Backend) *Service {
	return &Service{store: store, backend: backend}
}

type PrepareRequest struct {
	Owner                     string                  `json:"owner"`
	Agent                     string                  `json:"agent"`
	Action                    helper.DelegationAction `json:"action"`
	ReplaceExistingDelegate   bool                    `json:"replace_existing_delegate"`
	ExpectedPositionAuthority string                  `json:"expected_position_authority,omitempty"`
}

type Result struct {
	RequestID        string                  `json:"request_id"`
	Owner            string                  `json:"owner"`
	Action           helper.DelegationAction `json:"action"`
	TargetAuthority  string                  `json:"target_authority"`
	State            string                  `json:"state"`
	Signature        string                  `json:"signature,omitempty"`
	CurrentAuthority string                  `json:"current_authority,omitempty"`
	SigningRequest   *domain.SigningRequest  `json:"signing_request,omitempty"`
}

type record struct {
	Owner, Target, Before string
	Action                helper.DelegationAction
	Slot                  uint64
	Built                 helper.BuildDelegationResult
	Request               domain.SigningRequest
	State, Signature      string
}

type AuthorityStatus struct {
	Owner             string `json:"owner"`
	Agent             string `json:"agent"`
	PositionAuthority string `json:"position_authority"`
	Authorized        bool   `json:"authorized"`
}

// Status checks current authority independently of historical request IDs. It
// never registers an agent: owner-signed confirmation still owns that mutation.
func (s *Service) Status(ctx context.Context, owner, agent string) (AuthorityStatus, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if !validKey(owner) || !validKey(agent) || owner == agent {
		return AuthorityStatus{}, ErrInvalid
	}
	hash, err := s.backend.LatestBlockhash(ctx)
	if err != nil {
		return AuthorityStatus{}, err
	}
	built, err := s.backend.BuildDelegation(ctx, helper.BuildDelegationRequest{Action: helper.ActionRevoke, OwnerAuthority: owner, NewPositionAuthority: owner, FeePayer: s.backend.Address(), RecentBlockhash: hash.Hash, LastValidBlockHeight: hash.LastValidBlockHeight})
	if err != nil {
		return AuthorityStatus{}, err
	}
	a, err := s.backend.ReadAuthority(ctx, owner, built.TraderAccount, hash.Slot, false)
	if err != nil {
		return AuthorityStatus{}, err
	}
	var registered string
	err = s.store.reader.QueryRowContext(ctx, `SELECT agent_address FROM agent_authorizations WHERE venue='phoenix' AND owner_account=?`, owner).Scan(&registered)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return AuthorityStatus{}, err
	}
	return AuthorityStatus{Owner: owner, Agent: agent, PositionAuthority: a.PositionAuthority, Authorized: a.PositionAuthority == agent && registered == agent}, nil
}

func (r record) result() Result {
	v := Result{RequestID: r.Request.ID, Owner: r.Owner, Action: r.Action, TargetAuthority: r.Target, State: r.State, Signature: r.Signature}
	if r.State == "prepared" {
		req := r.Request
		v.SigningRequest = &req
		v.CurrentAuthority = r.Before
	}
	return v
}

func validKey(v string) bool {
	if len(v) > 44 {
		return false
	}
	b, e := base58.Decode(v)
	return e == nil && len(b) == 32
}

func (s *Service) Prepare(ctx context.Context, q PrepareRequest) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	q.Owner, q.Agent = strings.TrimSpace(q.Owner), strings.TrimSpace(q.Agent)
	if !validKey(q.Owner) || !validKey(q.Agent) || q.Owner == q.Agent || (q.Action != helper.ActionDelegate && q.Action != helper.ActionRevoke) {
		return Result{}, ErrInvalid
	}
	target := q.Agent
	if q.Action == helper.ActionRevoke {
		target = q.Owner
	}
	prior, err := s.store.load(ctx, q.Owner, "")
	if err != nil && !errors.Is(err, ErrNotFound) {
		return Result{}, err
	}
	if err == nil {
		if prior.State == "pending" {
			return prior.result(), ErrBusy
		}
		if prior.State == "prepared" && time.Now().Before(prior.Request.ExpiresAt) {
			if prior.Target == target && prior.Action == q.Action {
				return prior.result(), nil
			}
			return prior.result(), ErrBusy
		}
	}
	hash, err := s.backend.LatestBlockhash(ctx)
	if err != nil {
		return Result{}, err
	}
	built, err := s.backend.BuildDelegation(ctx, helper.BuildDelegationRequest{Action: q.Action, OwnerAuthority: q.Owner, NewPositionAuthority: target, FeePayer: s.backend.Address(), RecentBlockhash: hash.Hash, LastValidBlockHeight: hash.LastValidBlockHeight})
	if err != nil {
		return Result{}, err
	}
	current, err := s.backend.ReadAuthority(ctx, q.Owner, built.TraderAccount, hash.Slot, false)
	if err != nil {
		return Result{}, err
	}
	if q.Action == helper.ActionDelegate && current.PositionAuthority != q.Owner && current.PositionAuthority != target {
		if !q.ReplaceExistingDelegate || q.ExpectedPositionAuthority != current.PositionAuthority {
			return Result{CurrentAuthority: current.PositionAuthority}, ErrReplacementRequired
		}
	}
	if q.Action == helper.ActionRevoke && current.PositionAuthority != q.Agent && current.PositionAuthority != q.Owner {
		return Result{CurrentAuthority: current.PositionAuthority}, ErrAuthorityChanged
	}
	now := time.Now().UTC()
	id := uuid.NewString()
	payload, _ := json.Marshal(struct {
		MessageBase64 string                  `json:"message_base64"`
		Action        helper.DelegationAction `json:"action"`
		Owner         string                  `json:"owner_authority"`
		Target        string                  `json:"new_position_authority"`
		FeePayer      string                  `json:"fee_payer"`
		Trader        string                  `json:"trader_account"`
	}{built.MessageBase64, q.Action, q.Owner, target, s.backend.Address(), built.TraderAccount})
	metadata, _ := json.Marshal(built)
	r := record{Owner: q.Owner, Target: target, Before: current.PositionAuthority, Action: q.Action, Slot: max(hash.Slot, current.Slot), Built: built, State: "prepared",
		Request: domain.SigningRequest{ID: id, ClientOrderID: id, Venue: "phoenix", Action: string(q.Action), Account: q.Owner, Signer: q.Owner, UnsignedPayload: payload, VenueMetadata: metadata, CreatedAt: now, ExpiresAt: now.Add(30 * time.Second)}}
	if err = s.store.savePrepared(ctx, r); err != nil {
		return Result{}, err
	}
	return r.result(), nil
}

func (s *Service) Submit(ctx context.Context, owner string, signed domain.SignedAction) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if !validKey(owner) || signed.RequestID == "" {
		return Result{}, ErrInvalid
	}
	r, err := s.store.load(ctx, owner, signed.RequestID)
	if err != nil {
		return Result{}, err
	}
	if r.State != "prepared" {
		return r.result(), nil
	}
	if signed.RequestID != r.Request.ID || signed.Venue != "phoenix" || signed.ClientOrderID != r.Request.ClientOrderID || signed.SignerAddress != r.Owner || signed.Signature == "" {
		return Result{}, ErrInvalid
	}
	if !time.Now().Before(r.Request.ExpiresAt) {
		return s.finish(ctx, r, "expired")
	}
	current, err := s.backend.ReadAuthority(ctx, r.Owner, r.Built.TraderAccount, r.Slot, false)
	if err != nil {
		return r.result(), err
	}
	if current.PositionAuthority != r.Before && current.PositionAuthority != r.Target {
		result, err := s.finish(ctx, r, "failed")
		if err != nil {
			return result, err
		}
		return result, ErrAuthorityChanged
	}
	packet, err := s.backend.CoSignDelegation(ctx, r.Built, r.Owner, signed.Signature, r.Slot)
	if err != nil {
		return r.result(), err
	}
	if !time.Now().Before(r.Request.ExpiresAt) {
		return s.finish(ctx, r, "expired")
	}
	if len(packet) < 65 || packet[0] != 2 {
		return r.result(), ErrInvalid
	}
	signature := base58.Encode(packet[1:65])
	if err = s.store.transition(ctx, r, "prepared", "pending", signature); err != nil {
		return r.result(), err
	}
	r.State, r.Signature = "pending", signature
	if !time.Now().Before(r.Request.ExpiresAt) {
		return s.finish(ctx, r, "expired")
	}
	sent, sendErr := s.backend.SimulateAndSend(ctx, packet, r.Built.LastValidBlockHeight, r.Slot)
	if sendErr != nil && sent == "" {
		return s.finish(ctx, r, "failed")
	}
	// Pending was persisted before send. Lost/error responses never permit retry;
	// reconciliation uses the locally known signature, not an RPC response string.
	return r.result(), nil
}

func (s *Service) finish(ctx context.Context, r record, state string) (Result, error) {
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	if err := s.store.transition(writeCtx, r, r.State, state, r.Signature); err != nil {
		return r.result(), err
	}
	r.State = state
	return r.result(), nil
}

func (s *Service) Reconcile(ctx context.Context, owner, id string) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if !validKey(owner) || id == "" {
		return Result{}, ErrInvalid
	}
	r, err := s.store.load(ctx, owner, id)
	if err != nil {
		return Result{}, err
	}
	if r.State == "prepared" {
		if !time.Now().Before(r.Request.ExpiresAt) {
			return s.finish(ctx, r, "expired")
		}
		return r.result(), nil
	}
	if r.State != "pending" && r.State != "confirmed" {
		return r.result(), nil
	}
	minSlot := r.Slot
	if r.State == "pending" {
		status, err := s.backend.SignatureStatus(ctx, r.Signature)
		if err != nil {
			return r.result(), err
		}
		if status != nil {
			minSlot = max(minSlot, status.Slot)
			if string(status.Err) != "null" && (status.ConfirmationStatus == "confirmed" || status.ConfirmationStatus == "finalized") {
				return s.finish(ctx, r, "failed")
			}
		}
	}
	current, err := s.backend.ReadAuthority(ctx, r.Owner, r.Built.TraderAccount, minSlot, false)
	if err != nil {
		return r.result(), err
	}
	if current.PositionAuthority == r.Target {
		if r.State == "pending" {
			return s.finish(ctx, r, "confirmed")
		}
		result := r.result()
		result.CurrentAuthority = current.PositionAuthority
		return result, nil
	}
	if r.State == "confirmed" {
		return s.finish(ctx, r, "superseded")
	}
	validity, err := s.backend.FinalizedBlockhashValidity(ctx, r.Built.RecentBlockhash, r.Slot)
	if err != nil {
		return r.result(), err
	}
	if !validity.Valid {
		final, err := s.backend.ReadAuthority(ctx, r.Owner, r.Built.TraderAccount, max(minSlot, validity.Slot), true)
		if err != nil {
			return r.result(), err
		}
		if final.PositionAuthority == r.Target {
			if current.Slot > final.Slot {
				return s.finish(ctx, r, "superseded")
			}
			if current.Slot == final.Slot {
				return r.result(), rpc.ErrInvalidResponse
			}
			return s.finish(ctx, r, "confirmed")
		}
		return s.finish(ctx, r, "expired")
	}
	result := r.result()
	result.CurrentAuthority = current.PositionAuthority
	return result, nil
}
