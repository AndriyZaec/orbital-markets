import asterLogo from '@/assets/aster-logo.svg'
import hlLogo from '@/assets/hl-logo.svg'
import pacificaLogo from '@/assets/pacifica-logo.svg'

const VENUE_METADATA = {
  pacifica: {
    label: 'Pacifica',
    shortLabel: 'PAC',
    color: '#22d3ee',
    textColor: 'text-cyan-400',
    logo: pacificaLogo,
  },
  hyperliquid: {
    label: 'Hyperliquid',
    shortLabel: 'HL',
    color: '#a78bfa',
    textColor: 'text-violet-400',
    logo: hlLogo,
  },
  aster: {
    label: 'Aster',
    shortLabel: 'AST',
    color: '#f59e0b',
    textColor: 'text-amber-400',
    logo: asterLogo,
  },
} as const

export function venueMetadata(venue: string) {
  const normalized = venue.trim().toLowerCase()
  const known = VENUE_METADATA[normalized as keyof typeof VENUE_METADATA]
  if (known) return known

  const label = normalized
    ? normalized.charAt(0).toUpperCase() + normalized.slice(1)
    : 'Unknown'
  return {
    label,
    shortLabel: normalized.slice(0, 3).toUpperCase() || '--',
    color: '#f59e0b',
    textColor: 'text-muted-foreground',
    logo: null,
  }
}
