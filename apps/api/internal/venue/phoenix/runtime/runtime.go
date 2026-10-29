// Package runtime owns the optional Phoenix helper/RPC/relayer lifecycle.
// Infrastructure readiness does not imply account or live trading readiness.
package runtime

import (
	"context"
	"log/slog"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/helper"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/relayer"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/rpc"
)

type Config struct {
	HelperPath                string
	RPCURL                    string
	RelayerPrivateKey         string
	MaxTransactionFeeLamports uint64
}

type healthHelper interface {
	Health(context.Context) (helper.HealthResult, error)
	Close()
}

type chain interface {
	relayer.Chain
	LatestBlockhash(context.Context) (rpc.Blockhash, error)
}

type Runtime struct {
	helper  healthHelper
	chain   chain
	relayer *relayer.Service
	maxFee  uint64
	ctx     context.Context
	cancel  context.CancelFunc
	done    chan struct{}
}

func Start(ctx context.Context, config Config, logger *slog.Logger) (*Runtime, error) {
	signer, err := relayer.NewSigner(config.RelayerPrivateKey)
	if err != nil {
		return nil, err
	}
	client, err := rpc.New(config.RPCURL)
	if err != nil {
		return nil, err
	}
	service, err := relayer.NewService(signer, client, relayer.Policy{MaxTransactionFeeLamports: config.MaxTransactionFeeLamports}, logger)
	if err != nil {
		return nil, err
	}
	helperClient, err := helper.Start(ctx, config.HelperPath, 2*time.Second)
	if err != nil {
		return nil, err
	}
	return startRuntime(ctx, helperClient, client, service, config.MaxTransactionFeeLamports), nil
}

func startRuntime(ctx context.Context, h healthHelper, c chain, s *relayer.Service, maxFee uint64) *Runtime {
	ctx, cancel := context.WithCancel(ctx)
	r := &Runtime{helper: h, chain: c, relayer: s, maxFee: maxFee, ctx: ctx, cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(r.done)
		defer r.helper.Close()
		r.relayer.Run(ctx)
	}()
	return r
}

// Close stops balance polling and reaps the helper. Parent cancellation has the
// same effect; repeated Close calls wait on the same completed lifecycle.
func (r *Runtime) Close() { r.cancel(); <-r.done }

type Status struct {
	Ready      bool   `json:"ready"`
	Reason     string `json:"reason,omitempty"`
	BalanceLow bool   `json:"balance_low"`
}

// Readiness uses fresh reads, never a cached successful snapshot. The monitor
// warns independently at 0.05 SOL; infrastructure budget readiness requires
// enough SOL for one configured maximum-fee transaction. Actual signing gates
// the exact quoted fee. Flight registration and account readiness are separate.
func (r *Runtime) Readiness(ctx context.Context) Status {
	if r.ctx.Err() != nil {
		return Status{Reason: "stopped"}
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	health, err := r.helper.Health(ctx)
	if err != nil || health.HelperVersion == "" || health.PhoenixRiseVersion != helper.PhoenixRiseVersion {
		return Status{Reason: "helper_unavailable"}
	}
	if _, err := r.chain.LatestBlockhash(ctx); err != nil {
		return Status{Reason: "rpc_unavailable"}
	}
	balance, err := r.relayer.CheckBalance(ctx)
	if err != nil {
		return Status{Reason: "balance_unavailable"}
	}
	if r.ctx.Err() != nil {
		return Status{Reason: "stopped"}
	}
	if balance.Lamports < r.maxFee {
		return Status{Reason: "insufficient_fee_budget", BalanceLow: balance.Low}
	}
	return Status{Ready: true, BalanceLow: balance.Low}
}
