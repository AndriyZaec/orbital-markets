import type { TradingAgentStore } from '../src/agents/storage.ts'
import type { StoredTradingAgent, Venue } from '../src/agents/types.ts'

export class TestTradingAgentStore implements TradingAgentStore {
  readonly values = new Map<string, StoredTradingAgent>()
  readonly pendingValues = new Map<string, StoredTradingAgent>()

  ready(): Promise<void> { return Promise.resolve() }

  async save(agent: StoredTradingAgent): Promise<void> {
    this.values.set(key(agent.venue, agent.ownerAddress), structuredClone(agent))
  }

  async savePending(agent: StoredTradingAgent): Promise<void> {
    const storageKey = key(agent.venue, agent.ownerAddress)
    if (this.pendingValues.has(storageKey)) throw new Error('A pending trading agent already exists')
    this.pendingValues.set(storageKey, structuredClone(agent))
  }

  async promotePending(agent: StoredTradingAgent): Promise<void> {
    const pending = this.pendingValues.get(key(agent.venue, agent.ownerAddress))
    if (!pending || pending.agentAddress.toLowerCase() !== agent.agentAddress.toLowerCase()) {
      throw new Error('Pending trading agent changed before promotion')
    }
    await this.save(agent)
    await this.clearPending(agent.venue, agent.ownerAddress, agent.agentAddress)
  }

  async restore(venue: Venue, ownerAddress: string): Promise<Omit<StoredTradingAgent, 'privateKey'> | null> {
    const agent = this.values.get(key(venue, ownerAddress))
    if (!agent) return null
    return metadata(agent)
  }

  async loadForSigning(venue: Venue, ownerAddress: string): Promise<StoredTradingAgent | null> {
    return structuredClone(this.values.get(key(venue, ownerAddress)) ?? null)
  }

  async loadPendingForSigning(venue: Venue, ownerAddress: string): Promise<StoredTradingAgent | null> {
    return structuredClone(this.pendingValues.get(key(venue, ownerAddress)) ?? null)
  }

  async clearPending(venue: Venue, ownerAddress: string, expectedAgentAddress: string): Promise<void> {
    const storageKey = key(venue, ownerAddress)
    if (this.pendingValues.get(storageKey)?.agentAddress.toLowerCase() === expectedAgentAddress.toLowerCase()) {
      this.pendingValues.delete(storageKey)
    }
  }

  async clear(venue: Venue, ownerAddress: string): Promise<void> {
    this.values.delete(key(venue, ownerAddress))
  }
}

function metadata(agent: StoredTradingAgent): Omit<StoredTradingAgent, 'privateKey'> {
  return {
    version: agent.version,
    venue: agent.venue,
    ownerAddress: agent.ownerAddress,
    agentAddress: agent.agentAddress,
    authorizedAt: agent.authorizedAt,
    ...(agent.expiresAt ? { expiresAt: agent.expiresAt } : {}),
    ...(agent.builderAddress ? { builderAddress: agent.builderAddress } : {}),
    ...(agent.builderCode ? { builderCode: agent.builderCode } : {}),
  }
}

function key(venue: Venue, ownerAddress: string): string {
  const owner = venue === 'pacifica' ? ownerAddress : ownerAddress.toLowerCase()
  return `${venue}:${owner}`
}
