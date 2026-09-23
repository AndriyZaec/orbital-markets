import type { Address, Hex } from 'viem'

import { apiFetch, apiResponseError } from '../lib/api.ts'
import { buildAsterApproveAgentTypedData } from './aster-approve-agent.ts'

const ownerChainId = 56
const dataAgentLifetime = 365 * 24 * 60 * 60 * 1000
const dataAgentName = 'OrbitalData'

export interface AsterDataAgentApproval {
  user: Address
  nonce: number
  agentName: typeof dataAgentName
  agentAddress: Address
  ipWhitelist: ''
  expired: number
  canSpotTrade: false
  canPerpTrade: false
  canWithdraw: false
  asterChain: 'Mainnet'
  signatureChainId: typeof ownerChainId
}

export interface AsterDataAgentPreparation {
  probe_id: string
  approval: AsterDataAgentApproval
}

export type AsterDataAgentApprovalTypedData = ReturnType<typeof buildAsterDataAgentApprovalTypedData>

export function parseAsterDataAgentPreparation(
  value: unknown,
  expectedOwner: string,
): AsterDataAgentPreparation {
  if (!hasExactKeys(value, ['probe_id', 'approval'])) {
    throw new Error('Orbital returned an invalid Aster data-agent preparation response')
  }
  const preparation = value as Record<string, unknown>
  if (typeof preparation.probe_id !== 'string' || !preparation.probe_id) {
    throw new Error('Orbital returned an invalid Aster data-agent preparation response')
  }
  const approval = preparation.approval
  if (!hasExactKeys(approval, [
    'user', 'nonce', 'agentName', 'agentAddress', 'ipWhitelist', 'expired',
    'canSpotTrade', 'canPerpTrade', 'canWithdraw', 'asterChain', 'signatureChainId',
  ])) {
    throw new Error('Orbital returned an invalid Aster read-only approval')
  }
  const fields = approval as Record<string, unknown>
  if (!isAddress(fields.user) || fields.user.toLowerCase() !== expectedOwner.toLowerCase()) {
    throw new Error('Orbital returned a mismatched Aster approval owner')
  }
  const valid =
    isPositiveSafeInteger(fields.nonce) &&
    fields.agentName === dataAgentName &&
    isAddress(fields.agentAddress) && fields.ipWhitelist === '' &&
    isPositiveSafeInteger(fields.expired) &&
    fields.expired - Math.floor(fields.nonce / 1000) === dataAgentLifetime &&
    fields.canSpotTrade === false && fields.canPerpTrade === false && fields.canWithdraw === false &&
    fields.asterChain === 'Mainnet' && fields.signatureChainId === ownerChainId
  if (!valid) throw new Error('Orbital returned an invalid Aster read-only approval')

  return {
    probe_id: preparation.probe_id,
    approval: {
      user: fields.user,
      nonce: fields.nonce as number,
      agentName: dataAgentName,
      agentAddress: fields.agentAddress as Address,
      ipWhitelist: '',
      expired: fields.expired as number,
      canSpotTrade: false,
      canPerpTrade: false,
      canWithdraw: false,
      asterChain: 'Mainnet',
      signatureChainId: ownerChainId,
    },
  }
}

export function buildAsterDataAgentApprovalTypedData(approval: AsterDataAgentApproval) {
  return buildAsterApproveAgentTypedData(approval)
}

export async function prepareAsterDataAgentAuthorization(options: {
  account: string
  executionAgent: Address
  isCurrent: () => boolean | Promise<boolean>
}): Promise<AsterDataAgentPreparation> {
  const response = await currentStep(options.isCurrent, () => apiFetch('/api/v1/live/aster/data-agent/prepare', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ account: options.account, execution_agent: options.executionAgent }),
  }))
  if (!response.ok) {
    throw await currentStep(options.isCurrent, () => apiResponseError(
      response, 'Unable to prepare Aster read-only authorization.',
    ))
  }
  return parseAsterDataAgentPreparation(
    await currentStep(options.isCurrent, () => response.json()), options.account,
  )
}

export async function authorizeAsterDataAgent(options: {
  account: string
  executionAgent: Address
  preparation: AsterDataAgentPreparation
  signature: Hex
  isCurrent: () => boolean | Promise<boolean>
}): Promise<void> {
  const response = await currentStep(options.isCurrent, () => apiFetch('/api/v1/live/aster/data-agent/authorize', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      probe_id: options.preparation.probe_id,
      signature: options.signature,
      account: options.account,
      execution_agent: options.executionAgent,
      approval: options.preparation.approval,
    }),
  }))
  if (!response.ok) {
    throw await currentStep(options.isCurrent, () => apiResponseError(
      response, 'Unable to authorize Aster read-only access.',
    ))
  }
}

async function currentStep<T>(
  isCurrent: () => boolean | Promise<boolean>,
  step: () => Promise<T>,
): Promise<T> {
  await assertCurrent(isCurrent)
  try {
    return await step()
  } finally {
    await assertCurrent(isCurrent)
  }
}

async function assertCurrent(isCurrent: () => boolean | Promise<boolean>): Promise<void> {
  if (!await isCurrent()) throw new Error('Aster owner or execution agent changed during authorization')
}

function hasExactKeys(value: unknown, expected: string[]): value is Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return false
  const keys = Object.keys(value).sort()
  const sortedExpected = [...expected].sort()
  return keys.length === sortedExpected.length && keys.every((key, index) => key === sortedExpected[index])
}

function isAddress(value: unknown): value is Address {
  return typeof value === 'string' && /^0x[0-9a-fA-F]{40}$/.test(value)
}

function isPositiveSafeInteger(value: unknown): value is number {
  return typeof value === 'number' && Number.isSafeInteger(value) && value > 0
}
