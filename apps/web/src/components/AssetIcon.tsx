import { useState } from 'react'

const HYPERLIQUID_ICON_ALIASES: Record<string, string> = {
  KBONK: 'BONK',
  KPEPE: 'PEPE',
  KSHIB: 'SHIB',
}

const SIZES = {
  sm: {
    slot: 'size-6',
    image: 'size-5',
    fallback: 'size-6 text-[8px]',
  },
  md: {
    slot: 'size-8',
    image: 'size-7',
    fallback: 'size-7 text-[9px]',
  },
}

export function AssetIcon({ asset, size = 'md' }: { asset: string; size?: keyof typeof SIZES }) {
  const [failedAsset, setFailedAsset] = useState<string | null>(null)
  const unavailable = failedAsset === asset
  const iconAsset = HYPERLIQUID_ICON_ALIASES[asset.toUpperCase()] ?? asset
  const iconURL = `https://app.hyperliquid.xyz/coins/${encodeURIComponent(iconAsset)}.svg`
  const styles = SIZES[size]

  return (
    <span className={`flex shrink-0 items-center justify-center ${styles.slot}`}>
      {!unavailable ? (
        <img
          src={iconURL}
          alt=""
          loading="lazy"
          decoding="async"
          referrerPolicy="no-referrer"
          className={`${styles.image} rounded-sm object-contain`}
          onError={() => setFailedAsset(asset)}
        />
      ) : (
        <span className={`flex items-center justify-center rounded-full border border-white/[0.07] bg-white/[0.035] font-semibold uppercase text-muted-foreground ${styles.fallback}`}>
          {asset.slice(0, 2)}
        </span>
      )}
    </span>
  )
}
