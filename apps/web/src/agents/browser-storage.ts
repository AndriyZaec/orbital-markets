import { createTradingAgentStore, type TradingAgentStore } from './storage'

let persistentStore: TradingAgentStore | null = null

export function browserStorage(): TradingAgentStore {
  if (typeof window === 'undefined') throw new Error('Trading authorization requires a browser session')
  if (!persistentStore) {
    try {
      const storage = window.sessionStorage
      for (let index = storage.length - 1; index >= 0; index -= 1) {
        const key = storage.key(index)
        if (key?.startsWith('orbital.agent.') && key.includes('.v1:')) storage.removeItem(key)
      }
    } catch {
      // The encrypted vault can still work when legacy Web Storage is unavailable.
    }
    persistentStore = createTradingAgentStore(window.indexedDB, window.crypto)
  }
  return persistentStore
}
