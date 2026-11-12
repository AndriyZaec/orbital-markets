import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { assetIconUrls, fetchPhoenixAssetIcons } from '@/lib/asset-icons'

const DARK_ICON_STYLES: Record<string, string> = {
  MEGA: 'brightness-0 invert',
  NEAR: 'brightness-0 invert',
  WLD: 'brightness-0 invert',
  XPR: 'brightness-0 invert',
  XRP: 'brightness-0 invert',
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
  const [failedSource, setFailedSource] = useState<{ asset: string; index: number } | null>(null)
  const sourceIndex = failedSource?.asset === asset ? failedSource.index : 0
  const existingURLs = assetIconUrls(asset)
  const { data: phoenixIcons } = useQuery({
    queryKey: ['phoenix-asset-icons'],
    queryFn: fetchPhoenixAssetIcons,
    enabled: sourceIndex >= existingURLs.length,
    staleTime: 5 * 60_000,
    gcTime: 60 * 60_000,
    retry: false,
    refetchOnWindowFocus: false,
  })
  const iconURLs = assetIconUrls(asset, phoenixIcons?.[asset.toUpperCase()])
  const iconURL = iconURLs[sourceIndex]
  const styles = SIZES[size]
  const iconStyle = sourceIndex === 0 ? DARK_ICON_STYLES[asset.toUpperCase()] ?? '' : ''

  return (
    <span className={`flex shrink-0 items-center justify-center ${styles.slot}`}>
      {iconURL ? (
        <img
          key={iconURL}
          src={iconURL}
          alt=""
          loading="lazy"
          decoding="async"
          referrerPolicy="no-referrer"
          className={`${styles.image} rounded-sm object-contain ${iconStyle}`}
          onError={() => setFailedSource({ asset, index: sourceIndex + 1 })}
        />
      ) : (
        <span className={`flex items-center justify-center rounded-full border border-white/[0.07] bg-white/[0.035] font-semibold uppercase text-muted-foreground ${styles.fallback}`}>
          {asset.slice(0, 2)}
        </span>
      )}
    </span>
  )
}
