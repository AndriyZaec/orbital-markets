import { venueMetadata } from '@/lib/venue-metadata'

export function DetailVenueIcon({ venue }: { venue: string }) {
  const metadata = venueMetadata(venue)
  return (
    <span className="inline-flex size-7 items-center justify-center rounded border border-border bg-white/[0.04]" title={metadata.label}>
      {metadata.logo
        ? <img src={metadata.logo} alt={metadata.label} className="size-5" />
        : <span className="text-[11px] font-bold text-muted-foreground">{metadata.shortLabel[0]}</span>}
    </span>
  )
}

export function DetailStatItem({ label, value, mono, negative, children }: {
  label: string
  value?: string
  mono?: boolean
  negative?: boolean
  children?: React.ReactNode
}) {
  return (
    <div className="shrink-0">
      <p className="mb-0.5 text-[10px] text-muted-foreground">{label}</p>
      {children ?? <p className={`text-sm font-medium ${mono ? 'font-mono' : ''} ${negative ? 'text-red-400' : 'text-foreground'}`}>{value}</p>}
    </div>
  )
}
