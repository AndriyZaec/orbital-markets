import { useState, useEffect } from 'react'
import pacificaLogo from '@/assets/pacifica-logo.svg'
import hlLogo from '@/assets/hl-logo.svg'
import nadoLogo from '@/assets/nado.jpg'
import gmtradeLogo from '@/assets/gm-trade.png'
import driftLogo from '@/assets/drift.png'

type ConnectionStatus = 'disconnected' | 'ready' | 'coming_soon'

interface Venue {
  id: string
  name: string
  logo: string | null
  description: string
  initialStatus: ConnectionStatus
  chain: string
}

const VENUES: Venue[] = [
  {
    id: 'pacifica',
    name: 'Pacifica',
    logo: pacificaLogo,
    description: 'Solana-native perp DEX with on-chain settlement',
    initialStatus: 'disconnected',
    chain: 'Solana',
  },
  {
    id: 'hyperliquid',
    name: 'Hyperliquid',
    logo: hlLogo,
    description: 'High-performance L1 perp exchange',
    initialStatus: 'disconnected',
    chain: 'Hyperliquid L1',
  },
  {
    id: 'drift',
    name: 'Drift',
    logo: driftLogo,
    description: 'Solana perp and spot DEX with cross-margin',
    initialStatus: 'coming_soon',
    chain: 'Solana',
  },
  {
    id: 'nado',
    name: 'Nado',
    logo: nadoLogo,
    description: 'High-performance DEX built on the Ink Network',
    initialStatus: 'coming_soon',
    chain: 'Ink',
  },
  {
    id: 'gmtrade',
    name: 'GMTrade',
    logo: gmtradeLogo,
    description: 'Solana-based perpetual trading platform',
    initialStatus: 'coming_soon',
    chain: 'Solana',
  },
]

const STATUS_CONFIG: Record<ConnectionStatus, { label: string; dot: string; text: string }> = {
  disconnected: { label: 'Not Connected', dot: 'bg-zinc-500', text: 'text-muted-foreground' },
  ready: { label: 'Ready', dot: 'bg-green-400', text: 'text-green-400' },
  coming_soon: { label: 'Coming Soon', dot: 'bg-yellow-400/60', text: 'text-yellow-400/60' },
}

interface Props {
  open: boolean
  onConnectionChange?: (count: number) => void
  onClose: () => void
}

export function ConnectAccounts({ open, onConnectionChange, onClose }: Props) {
  const [statuses, setStatuses] = useState<Record<string, ConnectionStatus>>(() =>
    Object.fromEntries(VENUES.map((v) => [v.id, v.initialStatus]))
  )

  const connectedCount = Object.values(statuses).filter((s) => s === 'ready').length
  const totalSupported = VENUES.filter((v) => v.initialStatus !== 'coming_soon').length

  useEffect(() => {
    onConnectionChange?.(connectedCount)
  }, [connectedCount, onConnectionChange])

  const handleConnect = (venueId: string) => {
    setStatuses((prev) => ({ ...prev, [venueId]: 'ready' }))
  }

  const handleDisconnect = (venueId: string) => {
    setStatuses((prev) => ({ ...prev, [venueId]: 'disconnected' }))
  }

  return (
    <div
      className="border-l border-border bg-card flex flex-col shrink-0 transition-all duration-300 ease-in-out overflow-hidden"
      style={{ width: open ? 340 : 0, minWidth: open ? 340 : 0, opacity: open ? 1 : 0 }}
    >
      {/* Header */}
      <div className="flex items-center justify-between px-4 py-3 border-b border-border shrink-0">
        <div>
          <h2 className="text-sm font-semibold text-foreground">Connect Accounts</h2>
          <p className="text-[10px] text-muted-foreground/60 mt-0.5">{connectedCount}/{totalSupported} venues linked</p>
        </div>
        <button onClick={onClose} className="text-muted-foreground hover:text-foreground size-6 flex items-center justify-center rounded hover:bg-white/[0.06] transition-colors">
          <svg width="14" height="14" viewBox="0 0 16 16" fill="none"><path d="M4 4l8 8M12 4l-8 8" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round"/></svg>
        </button>
      </div>

      {/* Status summary */}
      <div className="px-4 py-3 border-b border-border shrink-0">
        <div className="flex items-center justify-between text-[11px]">
          <span className="text-muted-foreground">Status</span>
          <span className={`font-medium ${connectedCount === totalSupported ? 'text-green-400' : connectedCount > 0 ? 'text-yellow-400' : 'text-muted-foreground'}`}>
            {connectedCount === totalSupported ? 'Operational' : connectedCount > 0 ? 'Partial' : 'No accounts'}
          </span>
        </div>
        <div className="flex items-center justify-between text-[11px] mt-1.5">
          <span className="text-muted-foreground">Sync</span>
          <div className="flex items-center gap-1.5">
            <div className={`size-1.5 rounded-full ${connectedCount > 0 ? 'bg-green-400' : 'bg-zinc-500'}`} />
            <span className={`font-medium ${connectedCount > 0 ? 'text-green-400' : 'text-muted-foreground'}`}>
              {connectedCount > 0 ? 'Live' : 'Idle'}
            </span>
          </div>
        </div>
      </div>

      {/* Venue list */}
      <div className="flex-1 overflow-auto min-h-0 px-3 py-3">
        <p className="text-[10px] text-muted-foreground/50 uppercase tracking-wider font-medium mb-2 px-1">Venues</p>
        <div className="flex flex-col gap-2">
          {VENUES.map((venue) => {
            const status = statuses[venue.id]
            const cfg = STATUS_CONFIG[status]
            const isComingSoon = status === 'coming_soon'
            const isReady = status === 'ready'

            return (
              <div
                key={venue.id}
                className={`rounded-lg border px-3 py-3 ${
                  isReady
                    ? 'border-green-500/20 bg-green-500/[0.03]'
                    : isComingSoon
                      ? 'border-border bg-white/[0.01] opacity-50'
                      : 'border-border bg-white/[0.02]'
                }`}
              >
                <div className="flex items-center gap-2.5 mb-2">
                  <div className="size-8 rounded-md bg-white/[0.04] border border-border flex items-center justify-center shrink-0 overflow-hidden">
                    {venue.logo ? (
                      <img src={venue.logo} alt={venue.name} className="size-full object-cover" />
                    ) : (
                      <span className="text-xs font-bold text-muted-foreground">{venue.name[0]}</span>
                    )}
                  </div>
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-1.5">
                      <span className="text-xs font-semibold text-foreground">{venue.name}</span>
                      <span className="text-[9px] px-1 py-px rounded bg-white/[0.06] text-muted-foreground/70 font-medium">{venue.chain}</span>
                    </div>
                    <p className="text-[10px] text-muted-foreground/50 leading-snug mt-0.5 truncate">{venue.description}</p>
                  </div>
                </div>
                <div className="flex items-center justify-between">
                  <div className="flex items-center gap-1.5">
                    <div className={`size-1.5 rounded-full ${cfg.dot}`} />
                    <span className={`text-[10px] font-medium ${cfg.text}`}>{cfg.label}</span>
                  </div>
                  {isComingSoon ? (
                    <button disabled className="px-3 py-1 rounded text-[10px] font-medium bg-white/[0.04] text-muted-foreground/30 cursor-not-allowed">
                      Coming Soon
                    </button>
                  ) : isReady ? (
                    <button
                      onClick={() => handleDisconnect(venue.id)}
                      className="px-3 py-1 rounded text-[10px] font-medium bg-white/[0.06] text-muted-foreground hover:text-foreground hover:bg-white/[0.1] transition-colors"
                    >
                      Disconnect
                    </button>
                  ) : (
                    <button
                      onClick={() => handleConnect(venue.id)}
                      className="px-3 py-1 rounded text-[10px] font-medium bg-blue-600 text-white hover:bg-blue-500 transition-colors"
                    >
                      Connect
                    </button>
                  )}
                </div>
              </div>
            )
          })}
        </div>
      </div>

      {/* Footer note */}
      <div className="px-4 py-3 border-t border-border shrink-0">
        <div className="rounded border border-blue-500/10 bg-blue-500/[0.04] px-3 py-2 flex items-start gap-2">
          <svg width="12" height="12" viewBox="0 0 16 16" fill="none" className="text-blue-400/50 shrink-0 mt-px">
            <circle cx="8" cy="8" r="7" stroke="currentColor" strokeWidth="1.2"/>
            <path d="M8 7v4M8 5.5v.01" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round"/>
          </svg>
          <p className="text-[10px] text-blue-300/50 leading-relaxed">
            Orbital uses delegated access. Your credentials are never stored. Revoke anytime.
          </p>
        </div>
      </div>
    </div>
  )
}
