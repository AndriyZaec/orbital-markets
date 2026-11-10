import { act, cleanup, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { LivePositionsProvider, useLivePositions } from '../src/hooks/useLivePositions'

const mocks = vi.hoisted(() => ({ apiFetch: vi.fn(), subscribe: vi.fn(), visible: true,
  owners: { pacificaAddress: 'SolanaA', phoenixAddress: 'SolanaA', hyperliquidAddress: '0xEvm', asterAddress: '0xEvm' },
}))
vi.mock('@/lib/api', async (original) => ({ ...await original<typeof import('../src/lib/api')>(), apiFetch: mocks.apiFetch }))
vi.mock('@/hooks/useVenueAuthority', () => ({ useVenueAuthority: () => mocks.owners }))
vi.mock('@/hooks/usePageVisibility', () => ({ usePageVisibility: () => mocks.visible }))
vi.mock('@/lib/live-events', async (original) => ({ ...await original<typeof import('../src/lib/live-events')>(), subscribeLiveAccountEvents: mocks.subscribe }))

function Consumer({ name }: { name: string }) {
  const state = useLivePositions()
  return <div data-testid={name}>{state.positions.map((p) => p.id).join(',')}</div>
}

beforeEach(() => {
  vi.clearAllMocks()
  mocks.visible = true
  mocks.owners = { pacificaAddress: 'SolanaA', phoenixAddress: 'SolanaA', hyperliquidAddress: '0xEvm', asterAddress: '0xEvm' }
  mocks.apiFetch.mockImplementation(async () => Response.json([]))
  mocks.subscribe.mockImplementation(() => vi.fn())
})
afterEach(cleanup)

it('shares six pair feeds across panels and refreshes only the pair whose snapshot changed', async () => {
  const view = render(<LivePositionsProvider><Consumer name="trade" /></LivePositionsProvider>)
  await waitFor(() => expect(mocks.apiFetch).toHaveBeenCalledTimes(6))
  expect(mocks.subscribe).toHaveBeenCalledTimes(6)
  view.rerender(<LivePositionsProvider><Consumer name="trade" /><Consumer name="accounts" /></LivePositionsProvider>)
  await act(async () => {})
  expect(mocks.apiFetch).toHaveBeenCalledTimes(6)
  expect(mocks.subscribe).toHaveBeenCalledTimes(6)
  const [accounts, emit] = mocks.subscribe.mock.calls[0]
  const position = { id: 'position-A', state: 'open', started_at: '2026-11-10T12:00:00Z' }
  mocks.apiFetch.mockResolvedValueOnce(Response.json([position]))
  act(() => emit({ type: 'positions', data: [position] }))
  await waitFor(() => expect(screen.getByTestId('trade').textContent).toBe('position-A'))
  expect(screen.getByTestId('accounts').textContent).toBe('position-A')
  expect(mocks.apiFetch).toHaveBeenCalledTimes(7)
  const url = mocks.apiFetch.mock.calls[6][0]
  for (const venue of Object.keys(accounts)) expect(url).toContain(`account_${venue}=`)
  act(() => { emit({ type: 'disconnected' }); emit({ type: 'connected' }); emit({ type: 'positions', data: [position] }) })
  await act(async () => {})
  expect(mocks.apiFetch).toHaveBeenCalledTimes(7)
  mocks.visible = false
  view.rerender(<LivePositionsProvider><Consumer name="trade" /></LivePositionsProvider>)
  expect(mocks.subscribe).toHaveBeenCalledTimes(6)
  for (const result of mocks.subscribe.mock.results) expect(result.value).not.toHaveBeenCalled()
})

it('replaces only changed-owner pairs and ignores their late responses and events', async () => {
  const view = render(<LivePositionsProvider><Consumer name="trade" /></LivePositionsProvider>)
  await waitFor(() => expect(mocks.apiFetch).toHaveBeenCalledTimes(6))
  const old = mocks.subscribe.mock.calls.filter(([accounts]) => Object.values(accounts).includes('SolanaA'))
  mocks.owners = { ...mocks.owners, pacificaAddress: 'SolanaB', phoenixAddress: 'SolanaB' }
  view.rerender(<LivePositionsProvider><Consumer name="trade" /></LivePositionsProvider>)
  await waitFor(() => expect(mocks.apiFetch).toHaveBeenCalledTimes(11))
  expect(mocks.subscribe).toHaveBeenCalledTimes(11)
  act(() => old.forEach(([, emit]) => emit({ type: 'positions', data: [{ id: 'old-owner' }] })))
  await act(async () => {})
  expect(mocks.apiFetch).toHaveBeenCalledTimes(11)
  expect(screen.getByTestId('trade').textContent).toBe('')
})
