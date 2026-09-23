const venueTradeBases: Record<string, string> = {
  hyperliquid: 'https://app.hyperliquid.xyz/trade/',
  pacifica: 'https://app.pacifica.fi/trade/',
  aster: 'https://www.asterdex.com/en/trade/pro/futures/',
}

export function venueTradeUrl(venue: string, symbol: string): string | null {
  const normalizedVenue = venue.trim().toLowerCase()
  const base = venueTradeBases[normalizedVenue]
  let normalizedSymbol = symbol.trim().toUpperCase()
  if (!base || !normalizedSymbol) return null
  if (normalizedVenue === 'aster' && !normalizedSymbol.endsWith('USDT')) {
    normalizedSymbol += 'USDT'
  }
  return base + encodeURIComponent(normalizedSymbol)
}
