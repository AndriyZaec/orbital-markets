// Package runtime owns the optional Phoenix helper/RPC/relayer lifecycle.
// Infrastructure readiness does not imply account or live trading readiness.
package runtime

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/delegation"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/helper"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/relayer"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/rpc"
	"github.com/mr-tron/base58"
)

type Config struct {
	HelperPath                string
	RPCURL                    string
	RelayerPrivateKey         string
	MaxTransactionFeeLamports uint64
	BuilderAuthority          string
	OrderComputeBudget        helper.ComputeBudget
}

type healthHelper interface {
	Health(context.Context) (helper.HealthResult, error)
	Close()
	BuildDelegation(context.Context, helper.BuildDelegationRequest) (helper.BuildDelegationResult, error)
	DecodeTraderAuthority(context.Context, helper.DecodeAuthorityRequest) (helper.AuthorityResult, error)
	ComputeRisk(context.Context, helper.RiskRequest) (helper.RiskResult, error)
	NormalizeOrder(context.Context, helper.NormalizeRequest) (helper.NormalizedOrder, error)
	BuildOrder(context.Context, helper.BuildOrderRequest) (helper.BuildOrderResult, error)
	FlightAccounts(context.Context, helper.FlightRequest) (helper.FlightAccounts, error)
	VerifyFlight(context.Context, helper.VerifyFlightRequest) (helper.VerifiedFlight, error)
	DecodeAccounting(context.Context, helper.AccountingRequest) (helper.AccountingResult, error)
}

type chain interface {
	relayer.Chain
	LatestBlockhash(context.Context) (rpc.Blockhash, error)
	AccountInfo(context.Context, string, uint64, bool) (rpc.AccountInfo, error)
	SimulateAndSend(context.Context, []byte, uint64, uint64) (string, error)
	SignatureStatus(context.Context, string) (*rpc.SignatureStatus, error)
	FinalizedBlockhashValidity(context.Context, string, uint64) (rpc.BlockhashValidity, error)
}

var _ delegation.Backend = (*Runtime)(nil)

func (r *Runtime) Address() string { return r.relayer.Address() }
func (r *Runtime) LatestBlockhash(ctx context.Context) (rpc.Blockhash, error) {
	return r.chain.LatestBlockhash(ctx)
}
func (r *Runtime) BlockHeight(ctx context.Context, minSlot uint64) (uint64, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.chain.BlockHeight(ctx, minSlot)
}
func (r *Runtime) BuilderAuthority() string            { return r.builderAuthority }
func (r *Runtime) ComputeBudget() helper.ComputeBudget { return r.orderComputeBudget.Effective() }
func (r *Runtime) BuildDelegation(ctx context.Context, q helper.BuildDelegationRequest) (helper.BuildDelegationResult, error) {
	return r.helper.BuildDelegation(ctx, q)
}
func (r *Runtime) SignatureStatus(ctx context.Context, sig string) (*rpc.SignatureStatus, error) {
	return r.chain.SignatureStatus(ctx, sig)
}
func (r *Runtime) FinalizedBlockhashValidity(ctx context.Context, hash string, slot uint64) (rpc.BlockhashValidity, error) {
	return r.chain.FinalizedBlockhashValidity(ctx, hash, slot)
}
func (r *Runtime) CoSignDelegation(ctx context.Context, b helper.BuildDelegationResult, owner, sig string, slot uint64) ([]byte, error) {
	if err := r.ctx.Err(); err != nil {
		return nil, err
	}
	return r.relayer.CoSignDelegation(ctx, b, owner, sig, slot)
}
func (r *Runtime) CoSignOrder(ctx context.Context, b helper.BuildOrderResult, agent, sig string, slot uint64) ([]byte, error) {
	if err := r.ctx.Err(); err != nil {
		return nil, err
	}
	return r.relayer.CoSign(ctx, b, agent, sig, slot)
}
func (r *Runtime) SimulateAndSend(ctx context.Context, p []byte, height, slot uint64) (string, error) {
	if err := r.ctx.Err(); err != nil {
		return "", err
	}
	return r.chain.SimulateAndSend(ctx, p, height, slot)
}
func (r *Runtime) ReadAuthority(ctx context.Context, owner, trader string, slot uint64, finalized bool) (delegation.Authority, error) {
	a, err := r.chain.AccountInfo(ctx, trader, slot, finalized)
	if err != nil {
		return delegation.Authority{}, err
	}
	if a.Owner != helper.PhoenixProgramID {
		return delegation.Authority{}, rpc.ErrInvalidResponse
	}
	decoded, err := r.helper.DecodeTraderAuthority(ctx, helper.DecodeAuthorityRequest{OwnerAuthority: owner, TraderAccount: trader, AccountOwner: a.Owner, DataBase64: a.DataBase64})
	if err != nil {
		return delegation.Authority{}, err
	}
	return delegation.Authority{PositionAuthority: decoded.PositionAuthority, Slot: a.Slot}, nil
}

// Read-only helper frames must finish draining when an individual account feed
// is evicted. Cancelling an in-flight NDJSON frame otherwise stops the shared
// helper. Runtime shutdown still cancels it; helper calls retain their 2s bound.
func (r *Runtime) ComputeRisk(ctx context.Context, q helper.RiskRequest) (helper.RiskResult, error) {
	if err := ctx.Err(); err != nil {
		return helper.RiskResult{}, err
	}
	result, err := r.helper.ComputeRisk(r.ctx, q)
	if ctx.Err() != nil {
		return helper.RiskResult{}, ctx.Err()
	}
	return result, err
}

func (r *Runtime) NormalizeOrder(ctx context.Context, q helper.NormalizeRequest) (helper.NormalizedOrder, error) {
	if err := ctx.Err(); err != nil {
		return helper.NormalizedOrder{}, err
	}
	result, err := r.helper.NormalizeOrder(r.ctx, q)
	if ctx.Err() != nil {
		return helper.NormalizedOrder{}, ctx.Err()
	}
	return result, err
}

func (r *Runtime) BuildOrder(ctx context.Context, q helper.BuildOrderRequest) (helper.BuildOrderResult, error) {
	if q.ComputeBudget == (helper.ComputeBudget{}) {
		q.ComputeBudget = r.ComputeBudget()
	}
	if err := ctx.Err(); err != nil {
		return helper.BuildOrderResult{}, err
	}
	result, err := r.helper.BuildOrder(r.ctx, q)
	if ctx.Err() != nil {
		return helper.BuildOrderResult{}, ctx.Err()
	}
	return result, err
}

func (r *Runtime) AccountAuthority(ctx context.Context, owner string, minSlot uint64) (delegation.Authority, error) {
	if err := ctx.Err(); err != nil {
		return delegation.Authority{}, err
	}
	if err := r.ctx.Err(); err != nil {
		return delegation.Authority{}, err
	}
	hash, err := r.chain.LatestBlockhash(ctx)
	if err != nil {
		return delegation.Authority{}, err
	}
	built, err := r.helper.BuildDelegation(r.ctx, helper.BuildDelegationRequest{Action: helper.ActionRevoke, OwnerAuthority: owner, NewPositionAuthority: owner,
		FeePayer: r.Address(), RecentBlockhash: hash.Hash, LastValidBlockHeight: hash.LastValidBlockHeight})
	if err != nil {
		return delegation.Authority{}, err
	}
	if ctx.Err() != nil {
		return delegation.Authority{}, ctx.Err()
	}
	a, err := r.chain.AccountInfo(ctx, built.TraderAccount, max(minSlot, hash.Slot), false)
	if err != nil {
		return delegation.Authority{}, err
	}
	if a.Owner != helper.PhoenixProgramID || a.Slot < max(minSlot, hash.Slot) {
		return delegation.Authority{}, rpc.ErrInvalidResponse
	}
	if ctx.Err() != nil {
		return delegation.Authority{}, ctx.Err()
	}
	decoded, err := r.helper.DecodeTraderAuthority(r.ctx, helper.DecodeAuthorityRequest{OwnerAuthority: owner, TraderAccount: built.TraderAccount, AccountOwner: a.Owner, DataBase64: a.DataBase64})
	if err != nil {
		return delegation.Authority{}, err
	}
	if ctx.Err() != nil {
		return delegation.Authority{}, ctx.Err()
	}
	if r.ctx.Err() != nil {
		return delegation.Authority{}, errors.New("Phoenix runtime stopped")
	}
	return delegation.Authority{PositionAuthority: decoded.PositionAuthority, Slot: a.Slot}, nil
}

type Runtime struct {
	helper             healthHelper
	chain              chain
	relayer            *relayer.Service
	orderComputeBudget helper.ComputeBudget
	ctx                context.Context
	cancel             context.CancelFunc
	done               chan struct{}
	builderAuthority   string
	flightMu           sync.Mutex
	flightStatus       FlightStatus
}

func Start(ctx context.Context, config Config, logger *slog.Logger) (*Runtime, error) {
	budget := config.OrderComputeBudget.Effective()
	if err := budget.Validate(); err != nil {
		return nil, err
	}
	if config.BuilderAuthority != "" {
		key, err := base58.Decode(config.BuilderAuthority)
		if err != nil || len(key) != 32 {
			return nil, errors.New("invalid Phoenix Flight builder authority")
		}
	}
	signer, err := relayer.NewSigner(config.RelayerPrivateKey)
	if err != nil {
		return nil, err
	}
	client, err := rpc.New(config.RPCURL)
	if err != nil {
		return nil, err
	}
	service, err := relayer.NewService(signer, client, relayer.Policy{MaxTransactionFeeLamports: config.MaxTransactionFeeLamports, OrderComputeBudget: budget}, logger)
	if err != nil {
		return nil, err
	}
	helperClient, err := helper.Start(ctx, config.HelperPath, 2*time.Second)
	if err != nil {
		return nil, err
	}
	r := startRuntime(ctx, helperClient, client, service, config.BuilderAuthority)
	r.orderComputeBudget = budget
	return r, nil
}

func startRuntime(ctx context.Context, h healthHelper, c chain, s *relayer.Service, builder ...string) *Runtime {
	ctx, cancel := context.WithCancel(ctx)
	r := &Runtime{helper: h, chain: c, relayer: s, ctx: ctx, cancel: cancel, done: make(chan struct{})}
	if len(builder) > 0 {
		r.builderAuthority = builder[0]
	}
	go func() {
		defer close(r.done)
		defer r.helper.Close()
		<-ctx.Done()
	}()
	return r
}

// Close reaps the helper. Parent cancellation has the
// same effect; repeated Close calls wait on the same completed lifecycle.
func (r *Runtime) Close() { r.cancel(); <-r.done }

type Status struct {
	Ready                      bool   `json:"ready"`
	Reason                     string `json:"reason,omitempty"`
	ActionVerificationRequired bool   `json:"action_verification_required"`
}

// Readiness is local infrastructure availability, not chain admission. Opening
// a page or polling status must not read RPC. Every operation still checks its
// current authority, blockhash, Flight, exact fee and relayer balance on demand.
func (r *Runtime) Readiness(ctx context.Context) Status {
	if r.ctx.Err() != nil {
		return Status{Reason: "stopped"}
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if ctx.Err() != nil {
		return Status{Reason: "readiness_cancelled"}
	}
	// Drain shared helper frames across account eviction/request cancellation,
	// as with risk/order reads. The helper retains its own bounded call timeout.
	health, err := r.helper.Health(r.ctx)
	if ctx.Err() != nil {
		return Status{Reason: "readiness_cancelled"}
	}
	if err != nil || health.HelperVersion == "" || health.PhoenixRiseVersion != helper.PhoenixRiseVersion {
		return Status{Reason: "helper_unavailable"}
	}
	if r.ctx.Err() != nil {
		return Status{Reason: "stopped"}
	}
	return Status{Ready: true, ActionVerificationRequired: true}
}
