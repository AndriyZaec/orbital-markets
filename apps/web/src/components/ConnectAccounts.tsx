import { useEffect } from 'react'
import { useWallet } from '@solana/wallet-adapter-react'
import { useWalletModal } from '@solana/wallet-adapter-react-ui'
import { useAccount as useEvmAccount, useConnect as useEvmConnect, useDisconnect as useEvmDisconnect } from 'wagmi'
import { injected } from 'wagmi/connectors'
import { useVenueAuthority, type SigningReadiness } from '@/hooks/useVenueAuthority'
import pacificaLogo from '@/assets/pacifica-logo.svg'
import hlLogo from '@/assets/hl-logo.svg'
import nadoLogo from '@/assets/nado.jpg'
import gmtradeLogo from '@/assets/gm-trade.png'
import driftLogo from '@/assets/drift.png'

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

const READINESS_CONFIG: Record<SigningReadiness, { label: string; dot: string; text: string }> = {
  not_connected: { label: 'Not Connected', dot: 'bg-zinc-500', text: 'text-muted-foreground' },
  connected_cannot_sign: { label: 'No Signer', dot: 'bg-yellow-400', text: 'text-yellow-400' },
  ready: { label: 'Ready', dot: 'bg-green-400', text: 'text-green-400' },
  error: { label: 'Error', dot: 'bg-red-400', text: 'text-red-400' },
}

function truncateAddress(addr: string): string {
  if (addr.length <= 12) return addr
  return addr.slice(0, 6) + '...' + addr.slice(-4)
}

interface Props {
  open: boolean
  onConnectionChange?: (count: number) => void
  onClose: () => void
}

export function ConnectAccounts({ open, onConnectionChange, onClose }: Props) {
  const { venueAuthorities, pacifica, hyperliquid } = useVenueAuthority()

  const solWallet = useWallet()
  const { setVisible: setSolModalVisible } = useWalletModal()
  const evmAccount = useEvmAccount()
  const { connect: evmConnect } = useEvmConnect()
  const { disconnect: evmDisconnect } = useEvmDisconnect()

  const connectedCount = venueAuthorities.filter((v) => v.readiness === 'ready').length
  const totalSupported = VENUES.filter((v) => !v.comingSoon).length

  useEffect(() => {
    onConnectionChange?.(connectedCount)
  }, [connectedCount, onConnectionChange])

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

  const getVenueState = (venueId: string) => {
    if (venueId === 'pacifica') return pacifica
    if (venueId === 'hyperliquid') return hyperliquid
    return null
  }

  return (
    <div
      className="border-l border-border bg-card flex flex-col shrink-0 w-[340px] min-w-[340px] transition-[margin] duration-300 ease-in-out overflow-hidden"
      style={{ marginRight: open ? 0 : -340 }}
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
          <span className="text-muted-foreground">Signing Status</span>
          <span className={`font-medium ${connectedCount === totalSupported ? 'text-green-400' : connectedCount > 0 ? 'text-yellow-400' : 'text-muted-foreground'}`}>
            {connectedCount === totalSupported ? 'All Ready' : connectedCount > 0 ? 'Partial' : 'No accounts'}
          </span>
        </div>
        <div className="flex items-center justify-between text-[11px] mt-1.5">
          <span className="text-muted-foreground">Live Execution</span>
          <div className="flex items-center gap-1.5">
            <div className={`size-1.5 rounded-full ${connectedCount === totalSupported ? 'bg-green-400' : 'bg-zinc-500'}`} />
            <span className={`font-medium ${connectedCount === totalSupported ? 'text-green-400' : 'text-muted-foreground'}`}>
              {connectedCount === totalSupported ? 'Enabled' : 'Disabled'}
            </span>
          </div>
        </div>
      </div>

      {/* Venue list */}
      <div className="flex-1 overflow-auto min-h-0 px-3 py-3">
        <p className="text-[10px] text-muted-foreground/50 uppercase tracking-wider font-medium mb-2 px-1">Venues</p>
        <div className="flex flex-col gap-2">
          {VENUES.map((venue) => {
            const authority = getVenueState(venue.id)
            const isComingSoon = venue.comingSoon
            const readiness = authority?.readiness ?? 'not_connected'
            const isReady = readiness === 'ready'
            const cfg = isComingSoon
              ? { label: 'Coming Soon', dot: 'bg-yellow-400/60', text: 'text-yellow-400/60' }
              : READINESS_CONFIG[readiness]

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
                    {isReady && authority?.address ? (
                      <p className="text-[10px] text-green-400/70 font-mono leading-snug mt-0.5 truncate">{truncateAddress(authority.address)}</p>
                    ) : (
                      <p className="text-[10px] text-muted-foreground/50 leading-snug mt-0.5 truncate">{venue.description}</p>
                    )}
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
            Non-custodial signing. Your keys never leave your browser. Revoke anytime.
          </p>
        </div>
      </div>
    </div>
  )
}
