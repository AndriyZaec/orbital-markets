package relayer

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/helper"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/rpc"
)

const (
	LowBalanceLamports  = 50_000_000 // 0.05 SOL: warning only, not a trading cutoff
	balancePollInterval = 30 * time.Second
)

var (
	ErrFeeCap              = errors.New("Phoenix transaction network fee exceeds relayer cap")
	ErrInsufficientBalance = errors.New("Phoenix relayer SOL balance cannot cover transaction fee")
)

type Chain interface {
	BlockHeight(context.Context, uint64) (uint64, error)
	FeeForMessage(context.Context, []byte, uint64) (uint64, error)
	Balance(context.Context, string) (uint64, error)
}

type Policy struct {
	// Total network fee, including any Compute Budget priority fee. This is not
	// Phoenix/Flight's trading fee. Deployment must choose an explicit cap.
	MaxTransactionFeeLamports uint64
}

type Service struct {
	signer *Signer
	chain  Chain
	policy Policy
	logger *slog.Logger
}

func NewService(signer *Signer, chain Chain, policy Policy, logger *slog.Logger) (*Service, error) {
	if signer == nil || len(signer.key) == 0 || chain == nil || logger == nil || policy.MaxTransactionFeeLamports == 0 {
		return nil, errors.New("Phoenix relayer requires signer, chain, logger and positive network fee cap")
	}
	return &Service{signer: signer, chain: chain, policy: policy, logger: logger}, nil
}

type BalanceStatus struct {
	Lamports   uint64
	Low        bool
	ObservedAt time.Time
}

// CheckBalance always reads current RPC state; no cached success can authorize
// signing after a failed read. The warning threshold is independent of whether
// a specific transaction's fee is affordable.
func (s *Service) CheckBalance(ctx context.Context) (BalanceStatus, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	lamports, err := s.chain.Balance(ctx, s.signer.Address())
	if err != nil {
		return BalanceStatus{}, err
	}
	return BalanceStatus{Lamports: lamports, Low: lamports < LowBalanceLamports, ObservedAt: time.Now()}, nil
}

// Run monitors the configured fee payer until shutdown. It is opt-in: service
// construction starts no work. Signing still makes its own fresh balance read.
func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(balancePollInterval)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		balance, err := s.CheckBalance(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			s.logger.Warn("phoenix relayer balance unavailable", "err", err)
		} else if balance.Low {
			s.logger.Warn("phoenix relayer SOL balance low", "lamports", balance.Lamports, "warning_below_lamports", LowBalanceLamports)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// CoSign gates the low-level signer with fresh fee/balance/expiry checks. It
// does not submit, reserve SOL across sessions or replace live intent checks.
// minSlot comes from the server's blockhash context, not browser input.
func (s *Service) CoSign(ctx context.Context, built helper.BuildOrderResult, expectedAgent, signature string, minSlot uint64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	message, agentSignature, err := s.signer.verify(built, expectedAgent, signature)
	if err != nil {
		return nil, err
	}
	return s.gateAndAssemble(ctx, message, agentSignature, built.LastValidBlockHeight, minSlot)
}

// CoSignDelegation uses the same network fee, balance and expiry gates as orders,
// but requires the bound owner wallet's signature. It does not activate/revoke
// a browser agent; that requires on-chain authority confirmation by the caller.
func (s *Service) CoSignDelegation(ctx context.Context, built helper.BuildDelegationResult, expectedOwner, signature string, minSlot uint64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	message, ownerSignature, err := s.signer.verifyDelegation(built, expectedOwner, signature)
	if err != nil {
		return nil, err
	}
	return s.gateAndAssemble(ctx, message, ownerSignature, built.LastValidBlockHeight, minSlot)
}

func (s *Service) gateAndAssemble(ctx context.Context, message, signature []byte, lastValidHeight, minSlot uint64) ([]byte, error) {
	if lastValidHeight == 0 {
		return nil, ErrInvalidMessage
	}
	checkHeight := func() error {
		height, err := s.chain.BlockHeight(ctx, minSlot)
		if err != nil {
			return err
		}
		if height > lastValidHeight {
			return rpc.ErrExpired
		}
		return nil
	}
	if err := checkHeight(); err != nil {
		return nil, err
	}
	fee, err := s.chain.FeeForMessage(ctx, message, minSlot)
	if err != nil {
		return nil, err
	}
	if fee == 0 {
		return nil, rpc.ErrInvalidResponse
	}
	if fee > s.policy.MaxTransactionFeeLamports {
		return nil, ErrFeeCap
	}
	balance, err := s.CheckBalance(ctx)
	if err != nil {
		return nil, err
	}
	if balance.Lamports < fee {
		return nil, ErrInsufficientBalance
	}
	if err := checkHeight(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s.signer.assemble(message, signature), nil
}
