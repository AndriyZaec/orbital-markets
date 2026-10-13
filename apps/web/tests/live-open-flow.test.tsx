import type { ReactNode } from 'react'
import { act, cleanup, renderHook } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { LiveExecutionProvider, useLiveExecution } from '../src/hooks/useLiveExecution'
import type { ExecutionIntent } from '../src/lib/live-execution-state'
import type { SigningRequest } from '../src/types/signing'

const mocks = vi.hoisted(() => ({ apiFetch: vi.fn(), sign: vi.fn(),
  authority: { pacificaAddress: null as string | null, hyperliquidAddress: '0xhl-owner', asterAddress: '0xaster-owner' },
}))
vi.mock('@/lib/api', async (importOriginal) => ({
  ...await importOriginal<typeof import('../src/lib/api')>(), apiFetch: mocks.apiFetch,
}))
vi.mock('@/hooks/useVenueAuthority', () => ({ useVenueAuthority: () => mocks.authority }))
vi.mock('@/hooks/useTradingAgents', () => ({ useTradingAgents: () => ({
  pacifica: { status: 'ready', agentAddress: 'pac-agent' },
  hyperliquid: { status: 'ready', agentAddress: 'hl-agent' },
  aster: { status: 'ready', agentAddress: 'aster-agent' },
  sign: mocks.sign,
}) }))
vi.mock('@/lib/live-events', () => ({ subscribeLiveSessionEvents: () => () => {} }))

function wrapper({ children }: { children: ReactNode }) {
  return <LiveExecutionProvider>{children}</LiveExecutionProvider>
}

function intent(): ExecutionIntent {
  return {
    opportunityId: 'opp', asset: 'BTC', leverage: 2, requestedNotional: 100,
    approvedBaseAmount: 1, maxSlippagePct: 0.005, expiresAt: new Date(Date.now() + 300_000).toISOString(),
    legs: [
      { venue: 'aster', symbol: 'BTCUSDT', side: 'buy', expectedPrice: 100, limitPrice: 100.5 },
      { venue: 'hyperliquid', symbol: 'BTC', side: 'sell', expectedPrice: 100, limitPrice: 99.5 },
    ],
  }
}

function signingRequest(venue: 'aster' | 'hyperliquid', action: 'open' | 'unwind' | 'update_leverage', amount = 1): SigningRequest {
  return {
    id: `${venue}-${action}`, client_order_id: `${venue}-${action}`, venue, action,
    account: venue === 'aster' ? '0xaster-owner' : '0xhl-owner', symbol: venue === 'aster' ? 'BTCUSDT' : 'BTC',
    side: action === 'update_leverage' ? '' : venue === 'aster' && action === 'open' ? 'buy' : 'sell',
    amount, price: venue === 'aster' ? 100.5 : 100, limit_price: venue === 'aster' ? 100.5 : 99.5,
    reduce_only: action === 'unwind', leverage: 2, unsigned_payload: {},
    created_at: new Date().toISOString(), expires_at: new Date(Date.now() + 30_000).toISOString(),
  }
}

function prepareResponse() {
  const body = { session_id: 'session', asset: 'BTC', riskier_venue: 'aster', hedge_venue: 'hyperliquid',
    signing_requests: [signingRequest('hyperliquid', 'update_leverage'), signingRequest('aster', 'open'), signingRequest('aster', 'unwind')] }
  return new Response(
    [
      { type: 'progress', stage: 'quotes' },
      { type: 'progress', stage: 'accounts' },
      { type: 'result', status: 200, body },
    ].map((event) => JSON.stringify(event)).join('\n') + '\n',
    { headers: { 'Content-Type': 'application/x-ndjson' } },
  )
}

beforeEach(() => {
  vi.useFakeTimers()
  vi.setSystemTime(new Date('2026-10-13T12:00:00Z'))
  mocks.apiFetch.mockReset()
  mocks.authority = { pacificaAddress: null, hyperliquidAddress: '0xhl-owner', asterAddress: '0xaster-owner' }
  mocks.sign.mockReset().mockImplementation(async (request: SigningRequest) => ({
    request_id: request.id, client_order_id: request.client_order_id, venue: request.venue,
    signer_address: 'agent', signature: 'test-signature',
  }))
})
afterEach(() => { cleanup(); vi.useRealTimers() })

describe('prepare integration with the signing and advance flow', () => {
  it('times out before signing/submission and permits another Open', async () => {
    mocks.apiFetch.mockImplementationOnce(() => new Promise<Response>(() => {}))
    const { result } = renderHook(useLiveExecution, { wrapper })
    let execution: Promise<void>
    await act(async () => { execution = result.current.executeLive(intent()) })
    expect(result.current.state.phase).toBe('preparing')
    await act(async () => { await vi.advanceTimersByTimeAsync(20_001); await execution })
    expect(result.current.state.phase).toBe('failed')
    expect(result.current.state.error).toContain('No orders were submitted')
    expect(mocks.sign).not.toHaveBeenCalled()
    expect(mocks.apiFetch).toHaveBeenCalledTimes(1)

    mocks.apiFetch.mockResolvedValueOnce(Response.json({ error: 'account state not ready' }, { status: 409 }))
    await act(async () => { await result.current.executeLive(intent()) })
    expect(mocks.apiFetch).toHaveBeenCalledTimes(2)
    expect(result.current.state.error).toContain('account state not ready')
  })

  it('signs leverage/open/unwind, hedges the actual partial fill, and never applies prepare timeout after submission', async () => {
    let completeLeg1!: (response: Response) => void
    mocks.apiFetch
      .mockResolvedValueOnce(prepareResponse())
      .mockImplementationOnce(() => new Promise<Response>((resolve) => { completeLeg1 = resolve }))
      .mockResolvedValueOnce(Response.json({ status: 'open', position_id: 'position', mismatch: 0 }))
    const { result } = renderHook(useLiveExecution, { wrapper })
    let execution: Promise<void>
    await act(async () => { execution = result.current.executeLive(intent()) })
    expect(result.current.state.prepareStage).toBe('accounts')
    expect(result.current.state.phase).toBe('submitting_leg1')
    expect(JSON.parse(mocks.apiFetch.mock.calls[0][1].body).approved_price_limits).toEqual({ aster: 100.5, hyperliquid: 99.5 })
    expect(mocks.sign.mock.calls.map(([request]) => request.action)).toEqual(['update_leverage', 'open', 'unwind'])
    // Duplicate clicks cannot start a second prepare while leg 1 is in flight.
    await act(async () => { await result.current.executeLive(intent()) })
    expect(mocks.apiFetch).toHaveBeenCalledTimes(2)
    await act(async () => { await vi.advanceTimersByTimeAsync(65_000) })
    expect(result.current.state.phase).toBe('submitting_leg1')
    await act(async () => {
      completeLeg1(Response.json({ status: 'awaiting_leg2_sign',
        leg1_fill: { filled_amount: 0.6, avg_price: 100, status: 'filled' },
        signing_requests: [signingRequest('hyperliquid', 'open', 0.6)],
      }))
      await execution
    })
    expect(result.current.state.phase).toBe('open')
    expect(result.current.state.positionId).toBe('position')
    expect(mocks.sign.mock.calls.at(-1)?.[0].amount).toBe(0.6)
    expect(mocks.apiFetch.mock.calls.map(([path]) => path)).toEqual(['/api/v1/live/prepare', '/api/v1/live/advance', '/api/v1/live/advance'])
  })

  it('routes a lost leg-1 response to recovery instead of a clean failure or another prepare', async () => {
    mocks.apiFetch.mockResolvedValueOnce(prepareResponse()).mockRejectedValueOnce(new TypeError('Failed to fetch'))
    // Recovery fallback requests are intentionally left pending in this fixture.
    mocks.apiFetch.mockImplementation(() => new Promise<Response>(() => {}))
    const { result } = renderHook(useLiveExecution, { wrapper })
    await act(async () => { await result.current.executeLive(intent()) })
    expect(result.current.state.phase).toBe('recovering')
    expect(result.current.state.sessionId).toBe('session')
    expect(result.current.state.reason).toContain('Leg-1 response is uncertain')
  })

  it('requests armed unwind when the hedge signing fails, without submitting the hedge', async () => {
    mocks.apiFetch.mockResolvedValueOnce(prepareResponse())
      .mockResolvedValueOnce(Response.json({ status: 'awaiting_leg2_sign', leg1_fill: { filled_amount: .6, avg_price: 100, status: 'filled' }, signing_requests: [signingRequest('hyperliquid', 'open', .6)] }))
      .mockResolvedValueOnce(Response.json({ status: 'aborted', unwound: true, unwind_status: 'confirmed' }))
    const sign = mocks.sign.getMockImplementation()!
    mocks.sign.mockImplementation(async (request: SigningRequest) => {
      if (request.venue === 'hyperliquid' && request.action === 'open') throw new Error('local signing rejected')
      return sign(request)
    })
    const { result } = renderHook(useLiveExecution, { wrapper })
    await act(async () => { await result.current.executeLive(intent()) })
    expect(result.current.state.phase).toBe('aborted')
    expect(result.current.state.unwindStatus).toBe('confirmed')
    expect(mocks.apiFetch).toHaveBeenCalledTimes(3)
    expect(JSON.parse(mocks.apiFetch.mock.calls[2][1].body)).toMatchObject({ abort: true, accounts: { aster: '0xaster-owner', hyperliquid: '0xhl-owner' } })
    expect(JSON.parse(mocks.apiFetch.mock.calls[2][1].body).signed_actions).toBeUndefined()
  })

  it('does not sign a hedge beyond approval and aborts using the prepared accounts', async () => {
    mocks.apiFetch.mockResolvedValueOnce(prepareResponse())
      .mockResolvedValueOnce(Response.json({ status: 'awaiting_leg2_sign', signing_requests: [{ ...signingRequest('hyperliquid', 'open', .6), limit_price: 98 }] }))
      .mockResolvedValueOnce(Response.json({ status: 'aborted', unwound: true, unwind_status: 'confirmed' }))
    const { result } = renderHook(useLiveExecution, { wrapper })
    await act(async () => { await result.current.executeLive(intent()) })
    expect(result.current.state.guardFailure).toBe('price_moved')
    expect(mocks.sign.mock.calls.filter(([request]) => request.venue === 'hyperliquid' && request.action === 'open')).toHaveLength(0)
    expect(JSON.parse(mocks.apiFetch.mock.calls[2][1].body).abort).toBe(true)
  })

  it('routes a lost leg-2 response to recovery rather than retrying the order', async () => {
    mocks.apiFetch.mockResolvedValueOnce(prepareResponse())
      .mockResolvedValueOnce(Response.json({ status: 'awaiting_leg2_sign', signing_requests: [signingRequest('hyperliquid', 'open', .6)] }))
      .mockRejectedValueOnce(new TypeError('Failed to fetch'))
      .mockImplementation(() => new Promise<Response>(() => {}))
    const { result } = renderHook(useLiveExecution, { wrapper })
    await act(async () => { await result.current.executeLive(intent()) })
    expect(result.current.state.phase).toBe('recovering')
    expect(result.current.state.reason).toContain('Leg-2 response is uncertain')
    expect(mocks.apiFetch).toHaveBeenCalledTimes(3)
  })

  it('preserves degraded recovery when a wallet changes after leg 1 and unwind fails', async () => {
    let completeLeg1!: (response: Response) => void
    mocks.apiFetch.mockResolvedValueOnce(prepareResponse())
      .mockImplementationOnce(() => new Promise<Response>((resolve) => { completeLeg1 = resolve }))
      .mockResolvedValueOnce(Response.json({ status: 'degraded', unwound: false, unwind_status: 'submit_failed', reason: 'unwind rejected' }))
    const { result, rerender } = renderHook(useLiveExecution, { wrapper })
    let execution: Promise<void>
    await act(async () => { execution = result.current.executeLive(intent()) })
    await act(async () => { mocks.authority = { ...mocks.authority, asterAddress: '0xanother-owner' }; rerender() })
    await act(async () => { completeLeg1(Response.json({ status: 'awaiting_leg2_sign', signing_requests: [signingRequest('hyperliquid', 'open', .6)] })); await execution })
    expect(JSON.parse(mocks.apiFetch.mock.calls[2][1].body)).toMatchObject({ abort: true, accounts: { aster: '0xaster-owner' } })
    expect(result.current.state.phase).toBe('degraded')
    expect(result.current.state.unwindStatus).toBe('submit_failed')
  })

  it('preserves reported residual exposure when a wallet changes during hedge signing', async () => {
    let finishSigning!: (action: { request_id: string; client_order_id: string; venue: string; signer_address: string; signature: string }) => void
    mocks.apiFetch.mockResolvedValueOnce(prepareResponse())
      .mockResolvedValueOnce(Response.json({ status: 'awaiting_leg2_sign', signing_requests: [signingRequest('hyperliquid', 'open', .6)] }))
      .mockResolvedValueOnce(Response.json({ status: 'degraded', unwind_status: 'unconfirmed', remaining_exposure: [{ leg: 1, venue: 'aster', symbol: 'BTCUSDT', side: 'long', amount: .6 }] }))
    const sign = mocks.sign.getMockImplementation()!
    mocks.sign.mockImplementation(async (request: SigningRequest) => {
      if (request.venue === 'hyperliquid' && request.action === 'open') return new Promise((resolve) => { finishSigning = resolve })
      return sign(request)
    })
    const { result, rerender } = renderHook(useLiveExecution, { wrapper })
    let execution: Promise<void>
    await act(async () => { execution = result.current.executeLive(intent()) })
    expect(result.current.state.phase).toBe('awaiting_leg2')
    await act(async () => { mocks.authority = { ...mocks.authority, hyperliquidAddress: '0xanother-owner' }; rerender() })
    await act(async () => { finishSigning({ request_id: 'hyperliquid-open', client_order_id: 'hyperliquid-open', venue: 'hyperliquid', signer_address: 'agent', signature: 'fixture-signature' }); await execution })
    expect(result.current.state.phase).toBe('degraded')
    expect(result.current.state.remainingExposure).toHaveLength(1)
    expect(result.current.state.remainingExposure[0].amount).toBe(.6)
    expect(JSON.parse(mocks.apiFetch.mock.calls[2][1].body).abort).toBe(true)
  })

  it('keeps reconciliation active when leg-2 signing fails and the abort response is lost', async () => {
    mocks.apiFetch.mockResolvedValueOnce(prepareResponse())
      .mockResolvedValueOnce(Response.json({ status: 'awaiting_leg2_sign', signing_requests: [signingRequest('hyperliquid', 'open', .6)] }))
      .mockRejectedValueOnce(new TypeError('Failed to fetch'))
      .mockImplementation(() => new Promise<Response>(() => {}))
    const sign = mocks.sign.getMockImplementation()!
    mocks.sign.mockImplementation(async (request: SigningRequest) => {
      if (request.venue === 'hyperliquid' && request.action === 'open') throw new Error('local signing rejected')
      return sign(request)
    })
    const { result } = renderHook(useLiveExecution, { wrapper })
    await act(async () => { await result.current.executeLive(intent()) })
    expect(result.current.state.phase).toBe('recovering')
    expect(result.current.state.sessionId).toBe('session')
  })
})
