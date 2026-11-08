package runtime

import (
	"context"
	"errors"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/helper"
	phoenixlive "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/live"
)

const flightFreshness = 30 * time.Second

type FlightStatus struct {
	Ready            bool       `json:"ready"`
	Reason           string     `json:"reason,omitempty"`
	BuilderAuthority string     `json:"builder_authority,omitempty"`
	CheckedAt        *time.Time `json:"checked_at,omitempty"`
}

// FlightProof performs fresh confirmed RPC reads, fenced at least to the
// caller's blockhash/account slot. A cached diagnostic is never order admission.
// No signer or key belonging to the builder is needed.
func (r *Runtime) FlightProof(ctx context.Context, minSlot uint64) (phoenixlive.FlightProof, error) {
	proof, err := r.flightProof(ctx, minSlot)
	status := FlightStatus{BuilderAuthority: r.builderAuthority, Reason: "verification_unavailable"}
	if err == nil {
		at := proof.ObservedAt
		status = FlightStatus{Ready: true, BuilderAuthority: r.builderAuthority, CheckedAt: &at}
	}
	r.flightMu.Lock()
	r.flightStatus = status
	r.flightMu.Unlock()
	return proof, err
}

func (r *Runtime) flightProof(ctx context.Context, minSlot uint64) (phoenixlive.FlightProof, error) {
	if err := ctx.Err(); err != nil {
		return phoenixlive.FlightProof{}, err
	}
	if r.ctx.Err() != nil || r.builderAuthority == "" {
		return phoenixlive.FlightProof{}, errors.New("Phoenix Flight registration is not configured or runtime stopped")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	observed := time.Now().UTC()
	a, err := r.helper.FlightAccounts(r.ctx, helper.FlightRequest{BuilderAuthority: r.builderAuthority})
	if err != nil {
		return phoenixlive.FlightProof{}, err
	}
	if err := ctx.Err(); err != nil {
		return phoenixlive.FlightProof{}, err
	}
	if err := helper.ValidateFlightAccounts(r.builderAuthority, a); err != nil {
		return phoenixlive.FlightProof{}, err
	}
	hash, err := r.chain.LatestBlockhash(ctx)
	if err != nil {
		return phoenixlive.FlightProof{}, err
	}
	minSlot = max(minSlot, hash.Slot)
	if minSlot == 0 {
		return phoenixlive.FlightProof{}, errors.New("Phoenix Flight context slot unavailable")
	}
	q := helper.VerifyFlightRequest{BuilderAuthority: r.builderAuthority}
	slot := ^uint64(0)
	for _, read := range []struct {
		address, owner string
		out            *helper.FlightAccountData
	}{{a.GlobalState, helper.FlightProgramID, &q.GlobalState}, {a.BuilderState, helper.FlightProgramID, &q.BuilderState}, {a.BuilderTraderAccount, helper.PhoenixProgramID, &q.BuilderTrader}} {
		account, err := r.chain.AccountInfo(ctx, read.address, minSlot, false)
		if err != nil {
			return phoenixlive.FlightProof{}, err
		}
		if account.Owner != read.owner || account.Slot < minSlot {
			return phoenixlive.FlightProof{}, errors.New("Phoenix Flight account owner or context slot mismatch")
		}
		slot = min(slot, account.Slot) // Every account must cover the proof slot.
		*read.out = helper.FlightAccountData{Address: read.address, Owner: account.Owner, DataBase64: account.DataBase64}
	}
	if err := ctx.Err(); err != nil {
		return phoenixlive.FlightProof{}, err
	}
	verified, err := r.helper.VerifyFlight(r.ctx, q)
	if err != nil {
		return phoenixlive.FlightProof{}, err
	}
	if ctx.Err() != nil {
		return phoenixlive.FlightProof{}, ctx.Err()
	}
	if r.ctx.Err() != nil || verified.Accounts != a || !verified.Active || verified.FeeBPS != 2 || time.Since(observed) >= flightFreshness {
		return phoenixlive.FlightProof{}, errors.New("Phoenix Flight verification is invalid, stale or stopped")
	}
	return phoenixlive.FlightProof{ProgramOwner: a.FlightProgramID, BuilderAuthority: a.BuilderAuthority, BuilderState: a.BuilderState,
		BuilderTraderAccount: a.BuilderTraderAccount, FeeBPS: verified.FeeBPS, ObservedAt: observed, Slot: slot}, nil
}

// FlightReadiness is a bounded cached diagnostic, independently expiring after
// 30s. Delegation infrastructure readiness does not depend on Flight.
func (r *Runtime) FlightReadiness() FlightStatus {
	r.flightMu.Lock()
	status := r.flightStatus
	if status.CheckedAt != nil {
		at := *status.CheckedAt
		status.CheckedAt = &at
	}
	r.flightMu.Unlock()
	if r.ctx.Err() != nil {
		status.Ready, status.Reason = false, "stopped"
	} else if r.builderAuthority == "" {
		status.Ready, status.Reason = false, "not_configured"
	} else if status.CheckedAt == nil && status.Reason == "" {
		status.Reason = "action_verification_required"
	} else if status.Ready && (status.CheckedAt == nil || time.Now().Before(*status.CheckedAt) || time.Since(*status.CheckedAt) >= flightFreshness) {
		status.Ready, status.Reason = false, "verification_stale"
	}
	return status
}
