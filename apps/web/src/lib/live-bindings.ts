export type VenueAddressMap = Record<string, string>

export function liveVenueBindingsBody(
  accounts: VenueAddressMap,
  agents?: VenueAddressMap,
): Record<string, unknown> {
  const body: Record<string, unknown> = { accounts: { ...accounts } }
  const presentAgents = agents
    ? Object.fromEntries(Object.entries(agents).filter(([, agent]) => agent.trim() !== ''))
    : {}
  if (Object.keys(presentAgents).length > 0) body.agents = presentAgents

  if (accounts.pacifica) body.account_pacifica = accounts.pacifica
  if (accounts.hyperliquid) body.account_hyperliquid = accounts.hyperliquid
  if (presentAgents.pacifica) body.agent_pacifica = presentAgents.pacifica
  if (presentAgents.hyperliquid) body.agent_hyperliquid = presentAgents.hyperliquid
  return body
}

export function liveAccountsQuery(
  accounts: VenueAddressMap,
  extra: Record<string, string> = {},
): URLSearchParams {
  const query = new URLSearchParams()
  for (const [venue, account] of Object.entries(accounts).sort(([left], [right]) => left.localeCompare(right))) {
    query.set(`accounts[${venue}]`, account)
  }

  // Dual-write current aliases until all deployed API versions accept maps.
  if (accounts.pacifica) query.set('account_pacifica', accounts.pacifica)
  if (accounts.hyperliquid) query.set('account_hyperliquid', accounts.hyperliquid)
  for (const [key, value] of Object.entries(extra).sort(([left], [right]) => left.localeCompare(right))) {
    query.set(key, value)
  }
  return query
}

export function liveAccountsKey(accounts: VenueAddressMap): string {
  return JSON.stringify(Object.entries(accounts)
    .map(([venue, account]) => [venue, venue === 'hyperliquid' ? account.toLowerCase() : account] as const)
    .sort(([left], [right]) => left.localeCompare(right)))
}
