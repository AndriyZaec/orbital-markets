import { useEffect } from 'react'
import { useWallet } from '@solana/wallet-adapter-react'
import { useWalletModal } from '@solana/wallet-adapter-react-ui'
import { useConnect as useEvmConnect, useDisconnect as useEvmDisconnect } from 'wagmi'
import { injected } from 'wagmi/connectors'
import { useVenueReadiness, type VenueReadiness, type VenueId } from '@/hooks/useVenueReadiness'
import pacificaLogo from '@/assets/pacifica-logo.svg'
import hlLogo from '@/assets/hl-logo.svg'
import nadoLogo from '@/assets/nado.jpg'
import gmtradeLogo from '@/assets/gm-trade.png'
import driftLogo from '@/assets/drift.png'

// Static venue metadata (logos, blurbs, chain, coming-soon flag). Runtime
// readiness comes from useVenueReadiness — the single typed layer that
// composes wallet + signer + balance state.
interface VenueDef {
  id: string
  name: string
  logo: string | null
  description: string
  chain: string
  comingSoon: boolean
}

const VENUES: VenueDef[] = [
  { id: 'pacifica', name: 'Pacifica', logo: pacificaLogo, description: 'Solana-native perp DEX with on-chain settlement', chain: 'Solana', comingSoon: false },
  { id: 'hyperliquid', name: 'Hyperliquid', logo: hlLogo, description: 'High-performance L1 perp exchange', chain: 'Hyperliquid L1', comingSoon: false },
  { id: 'drift', name: 'Drift', logo: driftLogo, description: 'Solana perp and spot DEX with cross-margin', chain: 'Solana', comingSoon: true },
  { id: 'nado', name: 'Nado', logo: nadoLogo, description: 'High-performance DEX built on the Ink Network', chain: 'Ink', comingSoon: true },
  { id: 'gmtrade', name: 'GMTrade', logo: gmtradeLogo, description: 'Solana-based perpetual trading platform', chain: 'Solana', comingSoon: true },
]

interface Props {
  open: boolean
  onConnectionChange?: (count: number) => void
  onClose: () => void
}

function fmtUsd(n: number | null): string {
  if (n === null || !Number.isFinite(n)) return '--'
  if (Math.abs(n) >= 1_000_000) return `$${(n / 1_000_000).toFixed(2)}M`
  if (Math.abs(n) >= 1_000) return `$${(n / 1_000).toFixed(2)}K`
  return `$${n.toFixed(2)}`
}

// Diagnostic pill state (label + colors). Copy is operator-friendly, not
// adapter jargon: we say "Balance" not "balance stream", etc.
type PillTone = 'ok' | 'pending' | 'off' | 'bad'
const TONE: Record<PillTone, { dot: string; text: string }> = {
  ok:      { dot: 'bg-green-400',           text: 'text-green-400' },
  pending: { dot: 'bg-yellow-400',          text: 'text-yellow-400' },
  off:     { dot: 'bg-zinc-500',            text: 'text-muted-foreground' },
  bad:     { dot: 'bg-red-400',             text: 'text-red-400' },
}

function walletPill(r: VenueReadiness): { label: string; tone: PillTone } {
  if (r.status === 'error') return { label: 'Error', tone: 'bad' }
  return r.walletConnected ? { label: 'Connected', tone: 'ok' } : { label: 'Not connected', tone: 'off' }
}
function signerPill(r: VenueReadiness): { label: string; tone: PillTone } {
  if (!r.walletConnected) return { label: '—', tone: 'off' }
  return r.signerReady ? { label: 'Ready', tone: 'ok' } : { label: 'Missing', tone: 'pending' }
}
function balancePill(r: VenueReadiness): { label: string; tone: PillTone } {
  if (!r.walletConnected) return { label: '—', tone: 'off' }
  if (r.balanceReady) return { label: 'Ready', tone: 'ok' }
  if (r.balanceConnected) return { label: 'Pending', tone: 'pending' }
  return { label: 'Not connected', tone: 'off' }
}

// Overall aggregate → header color + copy. Kept local so the aggregate
// hook can stay purely data.
function aggregateTone(label: 'Ready' | 'Needs attention' | 'Not connected'): PillTone {
  if (label === 'Ready') return 'ok'
  if (label === 'Needs attention') return 'pending'
  return 'off'
}

export function ConnectAccounts({ open, onConnectionChange, onClose }: Props) {
  const { pacifica, hyperliquid, aggregate } = useVenueReadiness()

  const solWallet = useWallet()
  const { setVisible: setSolModalVisible } = useWalletModal()
  const { connect: evmConnect } = useEvmConnect()
  const { disconnect: evmDisconnect } = useEvmDisconnect()

  useEffect(() => {
    // Preserve existing parent contract: this counts fully-ready venues.
    onConnectionChange?.(aggregate.readyCount)
  }, [aggregate.readyCount, onConnectionChange])

  const handleConnect = (venueId: string) => {
    if (venueId === 'pacifica') {
      setSolModalVisible(true)
    } else if (venueId === 'hyperliquid') {
      evmConnect({ connector: injected() })
    }
  }

  const handleDisconnect = (venueId: string) => {
    if (venueId === 'pacifica') {
      solWallet.disconnect()
    } else if (venueId === 'hyperliquid') {
      evmDisconnect()
    }
  }

  const getReadiness = (venueId: string): VenueReadiness | null => {
    if (venueId === 'pacifica') return pacifica
    if (venueId === 'hyperliquid') return hyperliquid
    return null
  }

  const summaryTone = TONE[aggregateTone(aggregate.statusLabel)]

  return (
    <div
      className="border-l border-border bg-card flex flex-col shrink-0 w-[340px] min-w-[340px] transition-[margin] duration-300 ease-in-out overflow-hidden"
      style={{ marginRight: open ? 0 : -340 }}
    >
      {/* Header */}
      <div className="flex items-center justify-between px-4 py-3 border-b border-border shrink-0">
        <div>
          <h2 className="text-sm font-semibold text-foreground">Connect Accounts</h2>
          <p className="text-[10px] text-muted-foreground/60 mt-0.5">
            {aggregate.readyCount}/{aggregate.totalCount} venues ready
          </p>
        </div>
        <button onClick={onClose} className="text-muted-foreground hover:text-foreground size-6 flex items-center justify-center rounded hover:bg-white/[0.06] transition-colors">
          <svg width="14" height="14" viewBox="0 0 16 16" fill="none"><path d="M4 4l8 8M12 4l-8 8" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round"/></svg>
        </button>
      </div>

      {/* Aggregate readiness summary */}
      <div className="px-4 py-3 border-b border-border shrink-0">
        <div className="flex items-center justify-between text-[11px]">
          <span className="text-muted-foreground">Trading readiness</span>
          <div className="flex items-center gap-1.5">
            <div className={`size-1.5 rounded-full ${summaryTone.dot}`} />
            <span className={`font-medium ${summaryTone.text}`}>{aggregate.statusLabel}</span>
          </div>
        </div>
        {!aggregate.allReady && aggregate.blockingReasons.length > 0 && (
          <ul className="mt-2 flex flex-col gap-0.5">
            {aggregate.blockingReasons.map((r, i) => (
              <li key={i} className="text-[10px] text-muted-foreground/70 leading-snug">• {r}</li>
            ))}
          </ul>
        )}
      </div>

      {/* Venue list */}
      <div className="flex-1 overflow-auto min-h-0 px-3 py-3">
        <p className="text-[10px] text-muted-foreground/50 uppercase tracking-wider font-medium mb-2 px-1">Venues</p>
        <div className="flex flex-col gap-2">
          {VENUES.map((venue) => {
            const readiness = getReadiness(venue.id)
            const isComingSoon = venue.comingSoon
            const isReady = !isComingSoon && readiness?.status === 'ready'
            const isErr = !isComingSoon && readiness?.status === 'error'

            return (
              <div
                key={venue.id}
                className={`rounded-lg border px-3 py-3 ${
                  isReady
                    ? 'border-green-500/20 bg-green-500/[0.03]'
                    : isErr
                      ? 'border-red-500/25 bg-red-500/[0.03]'
                      : isComingSoon
                        ? 'border-border bg-white/[0.01] opacity-50'
                        : 'border-border bg-white/[0.02]'
                }`}
              >
                {/* Top row: logo + name + address/description */}
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
                    {readiness?.shortAddress ? (
                      <p className="text-[10px] text-muted-foreground/70 font-mono leading-snug mt-0.5 truncate">{readiness.shortAddress}</p>
                    ) : (
                      <p className="text-[10px] text-muted-foreground/50 leading-snug mt-0.5 truncate">{venue.description}</p>
                    )}
                  </div>
                </div>

                {/* Diagnostics — only for supported venues */}
                {!isComingSoon && readiness && (
                  <div className="mt-2 mb-2 rounded border border-border/60 bg-white/[0.02] px-2 py-2 flex flex-col gap-1">
                    <DiagRow label="Wallet" pill={walletPill(readiness)} />
                    <DiagRow label="Signer" pill={signerPill(readiness)} />
                    <DiagRow label="Balance" pill={balancePill(readiness)} />
                    {(readiness.equity !== null || readiness.available !== null) && (
                      <div className="flex items-center justify-between text-[10px] pt-1 mt-0.5 border-t border-border/40">
                        <span className="text-muted-foreground">Equity / Available</span>
                        <span className="font-mono text-foreground">
                          {fmtUsd(readiness.equity)} / {fmtUsd(readiness.available)}
                        </span>
                      </div>
                    )}
                    {readiness.blockingReasons.length > 0 && !isReady && (
                      <ul className="mt-1 flex flex-col gap-0.5">
                        {readiness.blockingReasons.map((r, i) => (
                          <li key={i} className="text-[10px] text-muted-foreground/70 leading-snug">• {r}</li>
                        ))}
                      </ul>
                    )}
                  </div>
                )}

                {/* Action */}
                <div className="flex items-center justify-end">
                  {isComingSoon ? (
                    <button disabled className="px-3 py-1 rounded text-[10px] font-medium bg-white/[0.04] text-muted-foreground/30 cursor-not-allowed">
                      Coming Soon
                    </button>
                  ) : readiness?.walletConnected ? (
                    <div className="flex items-center gap-1.5">
                      {!isReady && (
                        <button
                          onClick={() => handleConnect(venue.id)}
                          className="px-3 py-1 rounded text-[10px] font-medium bg-white/[0.06] text-muted-foreground hover:text-foreground hover:bg-white/[0.1] transition-colors"
                        >
                          Reconnect
                        </button>
                      )}
                      <button
                        onClick={() => handleDisconnect(venue.id)}
                        className="px-3 py-1 rounded text-[10px] font-medium bg-white/[0.06] text-muted-foreground hover:text-foreground hover:bg-white/[0.1] transition-colors"
                      >
                        Disconnect
                      </button>
                    </div>
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
            Non-custodial signing. Your keys never leave your browser. Revoke anytime.
          </p>
        </div>
      </div>
    </div>
  )
}

function DiagRow({ label, pill }: { label: string; pill: { label: string; tone: PillTone } }) {
  const t = TONE[pill.tone]
  return (
    <div className="flex items-center justify-between text-[10px]">
      <span className="text-muted-foreground">{label}</span>
      <div className="flex items-center gap-1.5">
        <div className={`size-1.5 rounded-full ${t.dot}`} />
        <span className={`font-medium ${t.text}`}>{pill.label}</span>
      </div>
    </div>
  )
}

// Suppress unused-VenueId warning if consumers of this file don't import it.
export type { VenueId }
