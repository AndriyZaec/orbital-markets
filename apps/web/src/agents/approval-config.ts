import { apiFetch, apiResponseError } from '../lib/api.ts'

// Loaded only for an explicit authorization. Venue-specific builders validate
// the returned name against their fixed production/staging allowlists.
export async function fetchAgentApprovalName(venue: 'hyperliquid' | 'aster'): Promise<unknown> {
  const response = await apiFetch(`/api/v1/live/agents/${venue}/approval-config`)
  if (!response.ok) {
    throw await apiResponseError(response, 'Unable to load trading agent authorization. Please try again.')
  }
  const body: unknown = await response.json()
  if (!body || typeof body !== 'object' || !('agent_name' in body)) {
    throw new Error('Orbital returned an invalid trading agent authorization')
  }
  return body.agent_name
}
