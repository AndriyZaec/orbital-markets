import { isHex, recoverTypedDataAddress, type Address, type Hex } from 'viem'

import { apiFetch, apiResponseError } from '../lib/api.ts'
import { buildAsterApproveAgentTypedData } from './aster-approve-agent.ts'

const ownerChainId = 56
const expiryTolerance = 60_000
const dataAgentLifetime = 365 * 24 * 60 * 60 * 1000
const pendingApprovalLifetime = 60_000
const dataAgentName = 'OrbitalData'

export type AsterDataAgentProbePhase = 'checking_status' | 'preparing' | 'wallet_signature' | 'validating_reads'

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

export interface AsterDataAgentProbeResult {
  agent_address: Address
  can_read: true
  can_spot_trade: false
  can_perp_trade: false
  can_withdraw: false
  expired: number
  endpoint_checks: {
    agent: true
    account: true
    position_risk: true
    income: true
  }
  execution_agent_preserved: true
}

export type AsterDataAgentLifecycleStatus = 'pending' | 'submitting' | 'approved' | 'rejected' | 'uncertain'

export interface AsterDataAgentReport {
  endpoints: { agent: boolean; account: boolean; position_risk: boolean; income: boolean }
  matched_agent_permissions?: {
    canRead: boolean; canSpotTrade: boolean; canPerpTrade: boolean; canWithdraw: boolean
  }
  requested_expiry: number
  reported_expiry?: number
  execution_agent_preserved: boolean
  success: boolean
  error?: string
}

export interface AsterDataAgentStatus {
  status: AsterDataAgentLifecycleStatus
  agent_address: Address
  requested_expiry: number
  last_result?: AsterDataAgentReport
  last_error?: string
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

export function parseAsterDataAgentStatus(value: unknown): AsterDataAgentStatus {
  if (!hasAllowedAndRequiredKeys(
    value,
    ['status', 'agent_address', 'requested_expiry'],
    ['last_result', 'last_error'],
  )) throw new Error('Orbital returned an invalid Aster data-agent status')
  const status = value as Record<string, unknown>
  const validStatuses: AsterDataAgentLifecycleStatus[] = [
    'pending', 'submitting', 'approved', 'rejected', 'uncertain',
  ]
  if (!validStatuses.includes(status.status as AsterDataAgentLifecycleStatus) ||
    !isAddress(status.agent_address) || !isPositiveSafeInteger(status.requested_expiry) ||
    (status.last_error !== undefined && (typeof status.last_error !== 'string' || !status.last_error))
  ) throw new Error('Orbital returned an invalid Aster data-agent status')

  const metadata = {
    agentAddress: status.agent_address,
    requestedExpiry: status.requested_expiry,
  }
  const lastResult = status.last_result === undefined
    ? undefined
    : parseAsterDataAgentReportPayload(status.last_result, metadata)
  return {
    status: status.status as AsterDataAgentLifecycleStatus,
    agent_address: status.agent_address,
    requested_expiry: status.requested_expiry,
    ...(lastResult ? { last_result: lastResult } : {}),
    ...(typeof status.last_error === 'string' ? { last_error: status.last_error } : {}),
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

export async function runAsterDataAgentProbe(options: {
  account: string
  executionAgent: string
  switchChain: (chainId: typeof ownerChainId) => Promise<unknown>
  signTypedData: (typedData: ReturnType<typeof buildAsterDataAgentApprovalTypedData>) => Promise<Hex>
  isCurrent: () => boolean | Promise<boolean>
  onPhase?: (phase: AsterDataAgentProbePhase) => void
  now?: () => number
}): Promise<AsterDataAgentProbeResult> {
  options.onPhase?.('checking_status')
  const statusResponse = await currentStep(options.isCurrent, () => apiFetch(
    `/api/v1/live/aster/data-agent/status?account=${encodeURIComponent(options.account)}&execution_agent=${encodeURIComponent(options.executionAgent)}`,
  ))
  if (statusResponse.status !== 404) {
    if (!statusResponse.ok) {
      throw await currentStep(options.isCurrent, () => apiResponseError(
        statusResponse, 'Unable to check the Aster read-only probe status.',
      ))
    }
    const status = parseAsterDataAgentStatus(
      await currentStep(options.isCurrent, () => statusResponse.json()),
    )
    if (status.status === 'approved') {
      options.onPhase?.('validating_reads')
      const runResponse = await currentStep(options.isCurrent, () => apiFetch('/api/v1/live/aster/data-agent/run', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ account: options.account, execution_agent: options.executionAgent }),
      }))
      if (!runResponse.ok) {
        throw await currentStep(options.isCurrent, () => apiResponseError(
          runResponse, 'Unable to run the approved Aster read-only probe.',
        ))
      }
      return parseAsterDataAgentReport(
        await currentStep(options.isCurrent, () => runResponse.json()),
        { agentAddress: status.agent_address, requestedExpiry: status.requested_expiry },
      )
    }
    const pendingExpiresAt = status.requested_expiry - dataAgentLifetime + pendingApprovalLifetime
    if (status.status !== 'pending' || pendingExpiresAt >= (options.now?.() ?? Date.now())) {
      throw new Error(terminalStatusError(status))
    }
  }

  options.onPhase?.('preparing')
  const preparedResponse = await currentStep(options.isCurrent, () => apiFetch('/api/v1/live/aster/data-agent/prepare', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ account: options.account, execution_agent: options.executionAgent }),
  }))
  if (!preparedResponse.ok) {
    throw await currentStep(options.isCurrent, () => apiResponseError(
      preparedResponse, 'Unable to prepare the Aster read-only probe.',
    ))
  }
  const prepared = parseAsterDataAgentPreparation(
    await currentStep(options.isCurrent, () => preparedResponse.json()), options.account,
  )

  await currentStep(options.isCurrent, () => options.switchChain(ownerChainId))
  options.onPhase?.('wallet_signature')
  const typedData = buildAsterDataAgentApprovalTypedData(prepared.approval)
  const signature = await currentStep(options.isCurrent, () => options.signTypedData(typedData))
  if (!isHex(signature) || !/^0x[0-9a-fA-F]{130}$/.test(signature)) {
    throw new Error('Aster owner wallet returned an invalid signature')
  }
  const recovered = await currentStep(
    options.isCurrent,
    () => recoverTypedDataAddress({ ...typedData, signature }),
  )
  if (recovered.toLowerCase() !== options.account.toLowerCase()) {
    throw new Error('Aster approval signature does not match the owner wallet')
  }

  options.onPhase?.('validating_reads')
  const validationResponse = await currentStep(options.isCurrent, () => apiFetch('/api/v1/live/aster/data-agent/validate', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      probe_id: prepared.probe_id,
      signature,
      account: options.account,
      execution_agent: options.executionAgent,
    }),
  }))
  if (!validationResponse.ok) {
    throw await currentStep(options.isCurrent, () => apiResponseError(
      validationResponse, 'Unable to validate the Aster read-only probe.',
    ))
  }
  return parseAsterDataAgentReport(
    await currentStep(options.isCurrent, () => validationResponse.json()),
    { agentAddress: prepared.approval.agentAddress, requestedExpiry: prepared.approval.expired },
  )
}

export function parseAsterDataAgentReport(
  value: unknown,
  metadata: { agentAddress: string; requestedExpiry: number },
): AsterDataAgentProbeResult {
  if (!isAddress(metadata.agentAddress) || !isPositiveSafeInteger(metadata.requestedExpiry)) {
    throw new Error('Orbital returned invalid Aster data-agent report metadata')
  }
  const result = parseAsterDataAgentReportPayload(value, metadata)
  if (!result.success) throw new Error(result.error as string)
  const permissions = result.matched_agent_permissions!
  if (!permissions.canRead) throw new Error('Aster data agent is not readable')
  if (permissions.canSpotTrade) throw new Error('Aster data agent unexpectedly allows spot trading')
  if (permissions.canPerpTrade) throw new Error('Aster data agent unexpectedly allows perpetual trading')
  if (permissions.canWithdraw) throw new Error('Aster data agent unexpectedly allows withdrawals')
  if (Math.abs(result.reported_expiry! - metadata.requestedExpiry) > expiryTolerance) {
    throw new Error('Aster data-agent expiry does not match the approval')
  }
  const failedEndpoint = Object.entries(result.endpoints).find(([, passed]) => !passed)?.[0]
  if (failedEndpoint) throw new Error(`Aster data-agent endpoint failed: ${failedEndpoint}`)
  if (!result.execution_agent_preserved) throw new Error('Aster execution agent was not preserved')
  return {
    agent_address: metadata.agentAddress,
    can_read: true,
    can_spot_trade: false,
    can_perp_trade: false,
    can_withdraw: false,
    expired: result.reported_expiry!,
    endpoint_checks: { agent: true, account: true, position_risk: true, income: true },
    execution_agent_preserved: true,
  }
}

function parseAsterDataAgentReportPayload(
  value: unknown,
  metadata: { agentAddress: string; requestedExpiry: number },
): AsterDataAgentReport {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    throw new Error('Orbital returned an invalid Aster data-agent probe result')
  }
  const result = value as Record<string, unknown>
  const allowedKeys = new Set([
    'endpoints', 'matched_agent_permissions', 'requested_expiry', 'reported_expiry',
    'execution_agent_preserved', 'success', 'error',
  ])
  const checks = result.endpoints
  const permissions = result.matched_agent_permissions
  const structurallyValid = Object.keys(result).every((key) => allowedKeys.has(key)) &&
    hasOwn(result, 'endpoints') && hasOwn(result, 'requested_expiry') &&
    hasOwn(result, 'execution_agent_preserved') && hasOwn(result, 'success') &&
    hasExactKeys(checks, ['agent', 'account', 'position_risk', 'income']) &&
    Object.values(checks as object).every((passed) => typeof passed === 'boolean') &&
    result.requested_expiry === metadata.requestedExpiry &&
    typeof result.execution_agent_preserved === 'boolean' && typeof result.success === 'boolean' &&
    (permissions === undefined || (
      hasExactKeys(permissions, ['canRead', 'canSpotTrade', 'canPerpTrade', 'canWithdraw']) &&
      Object.values(permissions).every((permission) => typeof permission === 'boolean')
    )) &&
    (result.reported_expiry === undefined || isPositiveSafeInteger(result.reported_expiry)) &&
    (result.error === undefined || (typeof result.error === 'string' && result.error.length > 0))
  if (!structurallyValid) throw new Error('Orbital returned an invalid Aster data-agent probe result')
  if (result.success && (result.error !== undefined || permissions === undefined || result.reported_expiry === undefined)) {
    throw new Error('Orbital returned an invalid Aster data-agent probe result')
  }
  if (!result.success && typeof result.error !== 'string') {
    throw new Error('Orbital returned an invalid Aster data-agent probe result')
  }
  return {
    endpoints: checks as AsterDataAgentReport['endpoints'],
    ...(permissions ? { matched_agent_permissions: permissions as AsterDataAgentReport['matched_agent_permissions'] } : {}),
    requested_expiry: result.requested_expiry as number,
    ...(typeof result.reported_expiry === 'number' ? { reported_expiry: result.reported_expiry } : {}),
    execution_agent_preserved: result.execution_agent_preserved as boolean,
    success: result.success as boolean,
    ...(typeof result.error === 'string' ? { error: result.error } : {}),
  }
}

function terminalStatusError(status: AsterDataAgentStatus): string {
  if (status.last_error) return status.last_error
  if (status.status === 'pending') return 'An Aster data-agent approval is pending; wait for it to expire before trying again'
  if (status.status === 'submitting') return 'Aster data-agent approval is submitting; check status before trying again'
  if (status.status === 'uncertain') return 'Aster data-agent approval outcome is uncertain; do not retry'
  if (status.status === 'rejected') return 'Aster data-agent approval was rejected; do not retry'
  return 'The previous Aster read-only probe failed; do not retry'
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

function hasOwn(value: object, key: string): boolean {
  return Object.prototype.hasOwnProperty.call(value, key)
}

function hasAllowedAndRequiredKeys(value: unknown, required: string[], optional: string[]): value is Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return false
  const keys = Object.keys(value)
  const allowed = new Set([...required, ...optional])
  return required.every((key) => hasOwn(value, key)) && keys.every((key) => allowed.has(key))
}

async function assertCurrent(isCurrent: () => boolean | Promise<boolean>): Promise<void> {
  if (!await isCurrent()) throw new Error('Aster owner or execution agent changed during the read-only probe')
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
