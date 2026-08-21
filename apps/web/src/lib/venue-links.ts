const venueTradeBases: Record<string, string> = {
  hyperliquid: 'https://app.hyperliquid.xyz/trade/',
  pacifica: 'https://app.pacifica.fi/trade/',
}

export function venueTradeUrl(venue: string, symbol: string): string | null {
  const base = venueTradeBases[venue.trim().toLowerCase()]
  const normalizedSymbol = symbol.trim().toUpperCase()
  if (!base || !normalizedSymbol) return null
  return base + encodeURIComponent(normalizedSymbol)
}
