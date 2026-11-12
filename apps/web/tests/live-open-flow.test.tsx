import type { ReactNode } from 'react'
import { act, cleanup, renderHook, screen } from '@testing-library/react'
import { renderWithQueryClient as render } from './render-with-query-client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { LiveExecutionProvider, useLiveExecution } from '../src/hooks/useLiveExecution'
import { useLiveClose } from '../src/hooks/useLiveClose'
import type { ExecutionIntent } from '../src/lib/live-execution-state'
import type { SigningRequest } from '../src/types/signing'
import { LiveExecutionModal } from '../src/components/LiveExecutionModal'

const mocks = vi.hoisted(() => ({ apiFetch: vi.fn(), sign: vi.fn(),
  sessionEvent: null as ((data: unknown) => void) | null,
  authority: { pacificaAddress: null as string | null, hyperliquidAddress: '0xhl-owner', asterAddress: '0xaster-owner', phoenixAddress: null as string | null },
}))
vi.mock('@/lib/api', async (importOriginal) => ({
  ...await importOriginal<typeof import('../src/lib/api')>(), apiFetch: mocks.apiFetch,
}))
vi.mock('@/hooks/useVenueAuthority', () => ({ useVenueAuthority: () => mocks.authority }))
vi.mock('@/hooks/useTradingAgents', () => ({ useTradingAgents: () => ({
  pacifica: { status: 'ready', agentAddress: 'pac-agent' },
  hyperliquid: { status: 'ready', agentAddress: 'hl-agent' },
  aster: { status: 'ready', agentAddress: 'aster-agent' },
  phoenix: { status: 'ready', agentAddress: 'phoenix-agent' },
  sign: mocks.sign,
}) }))
vi.mock('@/lib/live-events', () => ({ subscribeLiveSessionEvents: (
  _accounts: unknown, _sessionId: string, _connection: (connected: boolean) => void, listener: (data: unknown) => void,
) => { mocks.sessionEvent = listener; return () => { mocks.sessionEvent = null } } }))

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
  mocks.sessionEvent = null
  mocks.authority = { pacificaAddress: null, hyperliquidAddress: '0xhl-owner', asterAddress: '0xaster-owner', phoenixAddress: null }
  mocks.sign.mockReset().mockImplementation(async (request: SigningRequest) => ({
    request_id: request.id, client_order_id: request.client_order_id, venue: request.venue,
    signer_address: 'agent', signature: 'test-signature',
  }))
})
afterEach(() => { cleanup(); vi.useRealTimers() })

describe('prepare integration with the signing and advance flow', () => {
  it.each(['confirmed', 'unconfirmed'] as const)('normalizes a failed hedge response using %s unwind evidence', async (unwind_status) => {
    mocks.apiFetch.mockResolvedValueOnce(prepareResponse())
      .mockResolvedValueOnce(Response.json({ status: 'awaiting_leg2_sign', leg1_fill: { filled_amount: .6, avg_price: 100, status: 'filled' },
        signing_requests: [signingRequest('hyperliquid', 'open', .6)] }))
      .mockResolvedValueOnce(Response.json({ status: 'failed', unwind_status, remaining_exposure: [], leg1_fill: null }))
    const { result } = renderHook(useLiveExecution, { wrapper })
    await act(async () => { await result.current.executeLive(intent()) })
    expect(result.current.state.phase).toBe(unwind_status === 'confirmed' ? 'aborted' : 'degraded')
    expect(result.current.state.leg1Fill?.filled_amount).toBe(.6)
    expect(result.current.state.notStarted).not.toBe(true)
    expect(mocks.apiFetch).toHaveBeenCalledTimes(3)
  })

  it('keeps historical fill evidence when recovery SSE reports zero residuals with a failed status', async () => {
    mocks.apiFetch.mockResolvedValueOnce(prepareResponse())
      .mockResolvedValueOnce(Response.json({ status: 'awaiting_leg2_sign', leg1_fill: { filled_amount: .6, avg_price: 100, status: 'filled' },
        signing_requests: [signingRequest('hyperliquid', 'open', .6)] }))
      .mockRejectedValueOnce(new TypeError('Failed to fetch'))
    const { result } = renderHook(useLiveExecution, { wrapper })
    await act(async () => { await result.current.executeLive(intent()) })
    expect(result.current.state.phase).toBe('recovering')
    expect(mocks.sessionEvent).toBeTruthy()
    act(() => mocks.sessionEvent?.({ status: 'failed', remaining_exposure: [],
      leg1_fill: { filled_amount: 0, avg_price: 0, status: 'unconfirmed' } }))
    expect(result.current.state.phase).toBe('degraded')
    expect(result.current.state.leg1Fill?.filled_amount).toBe(.6)
    render(<LiveExecutionModal state={result.current.state} onRetry={vi.fn()} onClose={vi.fn()} onViewPositions={vi.fn()} />)
    expect(screen.queryByRole('button', { name: 'Try Again' })).toBeNull()
    expect(screen.getByRole('button', { name: 'Review Positions' })).toBeTruthy()
  })

  it.each([false, true])('shows a pre-submission admission refusal without raw reasons (stream=%s)', async (stream) => {
    const body = { error: 'live admission denied', code: 'LIVE_ADMISSION_DENIED', reasons: ['venue pair not in live scope'] }
    mocks.apiFetch.mockResolvedValueOnce(stream
      ? new Response(JSON.stringify({ type: 'result', status: 403, body }) + '\n', { headers: { 'Content-Type': 'application/x-ndjson' } })
      : Response.json(body, { status: 403 }))
    const { result } = renderHook(useLiveExecution, { wrapper })
    await act(async () => { await result.current.executeLive(intent()) })
    expect(result.current.state.notStarted).toBe(true)
    expect(result.current.state.error).toBe('Trade unavailable.')
    expect(mocks.sign).not.toHaveBeenCalled()
    expect(mocks.apiFetch).toHaveBeenCalledTimes(1)
    render(<LiveExecutionModal state={result.current.state} onRetry={vi.fn()} onClose={vi.fn()} onViewPositions={vi.fn()} />)
    expect(screen.getByText('Trade not started')).toBeTruthy()
    expect(screen.getByText('No orders were submitted.')).toBeTruthy()
    expect(screen.queryByText('Execution failed')).toBeNull()
    expect(screen.queryByText(/admission denied|live scope|Riskier/)).toBeNull()
  })

  it('prepares, signs and confirms a Phoenix close using its Solana account binding', async () => {
    mocks.authority.phoenixAddress = 'SolanaOwnerCaseSensitive'
    const request = { ...signingRequest('aster', 'unwind'), venue: 'phoenix', action: 'close', account: mocks.authority.phoenixAddress }
    mocks.apiFetch.mockResolvedValueOnce(Response.json({ signing_requests: [request] }))
      .mockResolvedValueOnce(Response.json({ accepted: true }))
      .mockResolvedValueOnce(Response.json({ position: { state: 'closed' } }))
    const { result } = renderHook(useLiveClose)
    await act(async () => { await result.current.closePosition('phoenix-position', ['phoenix', 'hyperliquid']) })
    expect(result.current.state.phase).toBe('done')
    expect(mocks.sign).toHaveBeenCalledWith(request)
    expect(JSON.parse(mocks.apiFetch.mock.calls[0][1].body)).toMatchObject({ accounts: { phoenix: 'SolanaOwnerCaseSensitive' }, agents: { phoenix: 'phoenix-agent' } })
    expect(mocks.apiFetch.mock.calls[2][0]).toContain('accounts%5Bphoenix%5D=SolanaOwnerCaseSensitive')
  })
  for (const ownerChanges of [false, true]) it(`uses Phoenix owner/agent bindings and ${ownerChanges ? 'aborts on a case-only Solana owner change' : 'opens through the shared sequential hedge flow'}`, async () => {
    mocks.authority.phoenixAddress = 'SolanaOwnerCaseSensitive'
    const approved = intent()
    approved.legs[0] = { ...approved.legs[0], venue: 'phoenix', symbol: 'BTC' }
    const phoenixRequest = (action: 'open' | 'unwind'): SigningRequest => ({ ...signingRequest('aster', action), venue: 'phoenix',
      id: `phoenix-${action}`, client_order_id: `phoenix-${action}`, account: mocks.authority.phoenixAddress!, symbol: 'BTC' })
    let completeLeg1!: (response: Response) => void
    mocks.apiFetch.mockResolvedValueOnce(Response.json({ session_id: 'session', asset: 'BTC', riskier_venue: 'phoenix', hedge_venue: 'hyperliquid',
      signing_requests: [phoenixRequest('open'), phoenixRequest('unwind')] }))
      .mockImplementationOnce(() => new Promise<Response>((resolve) => { completeLeg1 = resolve }))
      .mockResolvedValueOnce(Response.json(ownerChanges ? { status: 'degraded', unwind_status: 'requires_signature' } : { status: 'open', position_id: 'phoenix-position', mismatch: 0 }))
    const { result, rerender } = renderHook(useLiveExecution, { wrapper })
    let execution: Promise<void>
    await act(async () => { execution = result.current.executeLive(approved) })
    expect(result.current.state.phase).toBe('submitting_leg1')
    expect(JSON.parse(mocks.apiFetch.mock.calls[0][1].body)).toMatchObject({ accounts: { phoenix: 'SolanaOwnerCaseSensitive', hyperliquid: '0xhl-owner' }, agents: { phoenix: 'phoenix-agent' } })
    if (ownerChanges) await act(async () => { mocks.authority.phoenixAddress = 'solanaownercasesensitive'; rerender() })
    await act(async () => {
      completeLeg1(Response.json({ status: 'awaiting_leg2_sign', leg1_fill: { filled_amount: .6, avg_price: 100, status: 'filled' },
        signing_requests: [signingRequest('hyperliquid', 'open', .6)] }))
      await execution
    })
    const last = JSON.parse(mocks.apiFetch.mock.calls[2][1].body)
    expect(last.accounts.phoenix).toBe('SolanaOwnerCaseSensitive')
    if (ownerChanges) {
      expect(last.abort).toBe(true)
      expect(result.current.state.unwindStatus).toBe('requires_signature')
      expect(mocks.sign.mock.calls).toHaveLength(2)
    } else {
      expect(result.current.state.phase).toBe('open')
      expect(mocks.sign.mock.calls.at(-1)?.[0].amount).toBe(.6)
    }
  })
  it('keeps the modal preparing with a spinner while BBO is pending, without signing or another request', async () => {
    let stream!: ReadableStreamDefaultController<Uint8Array>
    const encoder = new TextEncoder()
    mocks.apiFetch.mockResolvedValueOnce(new Response(new ReadableStream<Uint8Array>({
      start(controller) {
        stream = controller
        controller.enqueue(encoder.encode(JSON.stringify({ type: 'progress', stage: 'waiting_bbo' }) + '\n'))
      },
    }), { headers: { 'Content-Type': 'application/x-ndjson' } }))
    const { result } = renderHook(useLiveExecution, { wrapper })
    let execution: Promise<void>
    await act(async () => { execution = result.current.executeLive(intent()) })
    expect(result.current.state.phase).toBe('preparing')
    expect(result.current.state.prepareStage).toBe('waiting_bbo')
    render(<LiveExecutionModal state={result.current.state} onRetry={vi.fn()} onClose={vi.fn()} onViewPositions={vi.fn()} />)
    expect(screen.getByText('Waiting for fresh market quotes')).toBeTruthy()
    expect(screen.getByRole('status', { name: 'Execution in progress' })).toBeTruthy()
    expect(screen.queryByText('Execution failed')).toBeNull()
    await act(async () => { await vi.advanceTimersByTimeAsync(5_000) })
    expect(result.current.state.phase).toBe('preparing')
    expect(mocks.sign).not.toHaveBeenCalled()
    expect(mocks.apiFetch).toHaveBeenCalledTimes(1)
    await act(async () => {
      stream.enqueue(encoder.encode(JSON.stringify({ type: 'result', status: 504,
        body: { error: 'Preparation timed out. Refresh the opportunity and try again.', code: 'PREPARE_TIMEOUT' },
      }) + '\n'))
      stream.close()
      await execution
    })
    expect(result.current.state.phase).toBe('failed')
    expect(mocks.sign).not.toHaveBeenCalled()
    expect(mocks.apiFetch).toHaveBeenCalledTimes(1)
  })

  it('times out before signing/submission and permits another Open', async () => {
    mocks.apiFetch.mockImplementationOnce(() => new Promise<Response>(() => {}))
    const { result } = renderHook(useLiveExecution, { wrapper })
    let execution: Promise<void>
    await act(async () => { execution = result.current.executeLive(intent()) })
    expect(result.current.state.phase).toBe('preparing')
    await act(async () => { await vi.advanceTimersByTimeAsync(20_001); await execution })
    expect(result.current.state.phase).toBe('failed')
    expect(result.current.state.notStarted).toBe(true)
    expect(result.current.state.error).toBe('Preparation timed out.')
    expect(mocks.sign).not.toHaveBeenCalled()
    expect(mocks.apiFetch).toHaveBeenCalledTimes(1)

    mocks.apiFetch.mockResolvedValueOnce(Response.json({ error: 'account state not ready', code: 'ACCOUNT_STATE_NOT_READY' }, { status: 409 }))
    await act(async () => { await result.current.executeLive(intent()) })
    expect(mocks.apiFetch).toHaveBeenCalledTimes(2)
    expect(result.current.state.error).toBe('Accounts are updating.')
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
    expect(result.current.state.notStarted).not.toBe(true)
    render(<LiveExecutionModal state={result.current.state} onRetry={vi.fn()} onClose={vi.fn()} onViewPositions={vi.fn()} />)
    expect(screen.queryByText('Trade not started')).toBeNull()
    expect(screen.queryByText('No orders were submitted.')).toBeNull()
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
