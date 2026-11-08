package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strconv"
	"strings"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/api"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/helper"
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
	units := strings.TrimSpace(getenv("PHOENIX_COMPUTE_UNIT_LIMIT"))
	price := strings.TrimSpace(getenv("PHOENIX_COMPUTE_UNIT_PRICE_MICRO_LAMPORTS"))
	if config.HelperPath == "" && config.RPCURL == "" && config.RelayerPrivateKey == "" && cap == "" && config.BuilderAuthority == "" && units == "" && price == "" {
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
	config.OrderComputeBudget = (helper.ComputeBudget{}).Effective()
	if units != "" {
		v, err := strconv.ParseUint(units, 10, 32)
		if err != nil || v == 0 {
			return phoenixruntime.Config{}, false, errors.New("invalid PHOENIX_COMPUTE_UNIT_LIMIT")
		}
		config.OrderComputeBudget.UnitLimit = uint32(v)
	}
	if price != "" {
		v, err := strconv.ParseUint(price, 10, 64)
		if err != nil {
			return phoenixruntime.Config{}, false, errors.New("invalid PHOENIX_COMPUTE_UNIT_PRICE_MICRO_LAMPORTS")
		}
		config.OrderComputeBudget.UnitPriceMicroLamports = v
	}
	if err := config.OrderComputeBudget.Validate(); err != nil {
		return phoenixruntime.Config{}, false, err
	}
	if config.OrderComputeBudget.PriorityFeeLamports() >= maxFee {
		return phoenixruntime.Config{}, false, errors.New("Phoenix priority fee leaves no room in total network fee cap")
	}
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
	logger.Info("Phoenix infrastructure started; live execution requires account and Flight readiness")
	return runtime
}
