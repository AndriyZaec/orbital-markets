const HYPERLIQUID_ICON_ALIASES: Record<string, string> = {
  KBONK: 'BONK',
  KPEPE: 'PEPE',
  KSHIB: 'SHIB',
}

const PACIFICA_ICON_FILES: Record<string, string> = {
  BP: 'BP.922b1f81fd98703c.svg',
  CL: 'CL.fa38d2be1e00fbf3.svg',
  COPPER: 'COPPER.8e879981e95f366b.svg',
  CRCL: 'CRCL.527b3903f44e6635.svg',
  DRAM: 'DRAM.7b99158fb005e60a.svg',
  EURUSD: 'EURUSD.d3b8c29dd87ae9d7.svg',
  GOOGL: 'GOOGL.20831dedaefe4d4b.svg',
  HOOD: 'HOOD.ae7ba5d7228a82d9.svg',
  MSTR: 'MSTR.93e30bb917424b76.svg',
  MU: 'MU.89c7ff4fe44e2983.svg',
  NATGAS: 'NATGAS.f892658d99f7e749.svg',
  NVDA: 'NVDA.49f7b6842bdf5b27.svg',
  PIPPIN: 'PIPPIN.f79ea86267cb4be7.svg',
  PLATINUM: 'PLATINUM.4e81d2ba2051c869.svg',
  PLTR: 'PLTR.2fbe0542a265c3b0.svg',
  SAMSUNG: 'SAMSUNG.0cdcdb61ad843a63.svg',
  SKHYNIX: 'SKHYNIX.d29838cb4c6099d0.svg',
  SNDK: 'SNDK.699f033c548c3abb.svg',
  SP500: 'SP500.012075fd856974aa.svg',
  SPCX: 'SPCX.495beb34f592e347.svg',
  TSLA: 'TSLA.02eefe3ea2ec90e7.svg',
  URNM: 'URNM.8d1a7a20890f2ca8.svg',
  USDJPY: 'USDJPY.6ebbd58765365515.svg',
  XAG: 'XAG.9acaafaffdf87a77.svg',
  XAU: 'XAU.1343ad2769b9e6b7.svg',
}

export function assetIconUrls(asset: string, phoenixIconUrl?: string): string[] {
  const normalizedAsset = asset.toUpperCase()
  const hyperliquidAsset = HYPERLIQUID_ICON_ALIASES[normalizedAsset] ?? normalizedAsset
  const urls = [`https://app.hyperliquid.xyz/coins/${encodeURIComponent(hyperliquidAsset)}.svg`]
  const pacificaFile = PACIFICA_ICON_FILES[normalizedAsset]
  if (pacificaFile) {
    urls.push(`https://app.pacifica.fi/imgs/optimized/tokens/${encodeURIComponent(pacificaFile)}`)
  }
  if (phoenixIconUrl) urls.push(phoenixIconUrl)
  return urls
}

export function parsePhoenixAssetIcons(payload: unknown): Record<string, string> {
  const icons: Record<string, string> = {}
  if (!payload || typeof payload !== 'object' || !('markets' in payload) || !Array.isArray(payload.markets)) return icons
  for (const market of payload.markets as unknown[]) {
    if (!market || typeof market !== 'object' || !('symbol' in market) || typeof market.symbol !== 'string' || !('metadata' in market)) continue
    const metadata = market.metadata
    if (!metadata || typeof metadata !== 'object' || !('logoUri' in metadata) || typeof metadata.logoUri !== 'string') continue
    const asset = market.symbol.replace(/-PERP$/, '').toUpperCase()
    if (!asset) continue
    try {
      const url = new URL(metadata.logoUri)
      if (url.protocol !== 'https:' || url.hostname !== 'phoenixcdn.trade' || url.username || url.password) continue
      icons[asset] = url.href
    } catch {
      // Optional presentation metadata must not prevent other icons loading.
    }
  }
  return icons
}

export async function fetchPhoenixAssetIcons({ signal }: { signal: AbortSignal }): Promise<Record<string, string>> {
  try {
    const response = await fetch('https://perp-api.phoenix.trade/v1/view/exchange', {
      signal: AbortSignal.any([signal, AbortSignal.timeout(10_000)]),
      credentials: 'omit',
      referrerPolicy: 'no-referrer',
    })
    if (!response.ok) return {}
    return parsePhoenixAssetIcons(await response.json())
  } catch {
    // Cache failures as an empty catalog too, rather than retrying for every row.
    return {}
  }
}
