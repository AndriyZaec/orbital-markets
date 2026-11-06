package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strconv"
	"strings"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/api"
	phoenixruntime "github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/runtime"
)

var _ api.PhoenixOrderBackend = (*phoenixruntime.Runtime)(nil)
var _ api.PhoenixSubmitBackend = (*phoenixruntime.Runtime)(nil)

func phoenixConfig(getenv func(string) string) (phoenixruntime.Config, bool, error) {
	config := phoenixruntime.Config{
		HelperPath:        strings.TrimSpace(getenv("PHOENIX_HELPER_PATH")),
		RPCURL:            strings.TrimSpace(getenv("HELIUS_RPC_URL")),
		RelayerPrivateKey: strings.TrimSpace(getenv("PHOENIX_RELAYER_PRIVATE_KEY")),
		BuilderAuthority:  strings.TrimSpace(getenv("PHOENIX_BUILDER_AUTHORITY")),
	}
	cap := strings.TrimSpace(getenv("PHOENIX_MAX_TRANSACTION_FEE_LAMPORTS"))
	if config.HelperPath == "" && config.RPCURL == "" && config.RelayerPrivateKey == "" && cap == "" && config.BuilderAuthority == "" {
		return config, false, nil
	}
	if config.RPCURL == "" || config.RelayerPrivateKey == "" || cap == "" {
		return phoenixruntime.Config{}, false, errors.New("Phoenix infrastructure requires HELIUS_RPC_URL, PHOENIX_RELAYER_PRIVATE_KEY and PHOENIX_MAX_TRANSACTION_FEE_LAMPORTS")
	}
	maxFee, err := strconv.ParseUint(cap, 10, 64)
	if err != nil || maxFee == 0 {
		return phoenixruntime.Config{}, false, errors.New("PHOENIX_MAX_TRANSACTION_FEE_LAMPORTS must be a positive integer")
	}
	config.MaxTransactionFeeLamports = maxFee
	if config.HelperPath == "" {
		config.HelperPath = "/phoenix-helper"
	}
	return config, true, nil
}

func startPhoenixInfrastructure(ctx context.Context, logger *slog.Logger) *phoenixruntime.Runtime {
	config, enabled, err := phoenixConfig(os.Getenv)
	if err != nil {
		logger.Warn("Phoenix infrastructure disabled", "reason", err)
		return nil
	}
	if !enabled {
		return nil
	}
	runtime, err := phoenixruntime.Start(ctx, config, logger)
	if err != nil {
		logger.Warn("Phoenix infrastructure disabled", "reason", err)
		return nil
	}
	logger.Info("Phoenix infrastructure started; live execution remains unavailable")
	return runtime
}
