import type { Address } from 'viem'

import builderConfig from '../../../api/internal/venue/hyperliquid/live/builder_config.json' with { type: 'json' }

export const asterBuilderAddress = builderConfig.address.toLowerCase() as Address
export const asterBuilderFeeRate = String(builderConfig.fee / 100_000)
export const hyperliquidBuilderAddress = builderConfig.address as Address
export const hyperliquidBuilderFee = builderConfig.fee
export const hyperliquidBuilderMaxFeeRate = builderConfig.maxFeeRate
