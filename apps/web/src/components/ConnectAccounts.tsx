import { useEffect, useRef, useState } from 'react'
import { useConnectVenue } from '@/accounts/useConnectVenue'
import { useVenueReadiness, type VenueReadiness, type VenueId } from '@/hooks/useVenueReadiness'
import { useTradingAgents } from '@/hooks/useTradingAgents'
import { useLiveExecution } from '@/hooks/useLiveExecution'
import { useLivePositions } from '@/hooks/useLivePositions'
import { useTelegramLink } from '@/hooks/useTelegramLink'
import { trackAnalytics } from '@/lib/analytics'
import { hasActiveLiveExposure } from '@/lib/live-events'
import { venueMetadata } from '@/lib/venue-metadata'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import telegramLogo from '@/assets/telegram-logo.svg'
import { PhoenixAuthorization } from './PhoenixAuthorization'
import { AccountBalance } from './AccountBalance'
import { accountActionStyles } from './account-action-styles'

// Static venue presentation. Runtime
// readiness comes from useVenueReadiness — the single typed layer that
// composes wallet + signer + balance state.
interface VenueDef {
  id: VenueId
  name: string
  logo: string | null
  preview?: boolean
}

const VENUES: VenueDef[] = [
  { id: 'pacifica', name: venueMetadata('pacifica').label, logo: venueMetadata('pacifica').logo },
  { id: 'hyperliquid', name: venueMetadata('hyperliquid').label, logo: venueMetadata('hyperliquid').logo },
  { id: 'aster', name: venueMetadata('aster').label, logo: venueMetadata('aster').logo },
  { id: 'phoenix', name: venueMetadata('phoenix').label, logo: venueMetadata('phoenix').logo },
]

interface Props {
  open: boolean
  onConnectionChange?: (count: number) => void
  onClose: () => void
}

// Diagnostic pill state (label + colors). Copy is operator-friendly, not
// adapter jargon: we say "Balance" not "balance stream", etc.
type PillTone = 'ok' | 'pending' | 'off' | 'bad'
interface DiagnosticPill {
  label: string
  tone: PillTone
  loading?: boolean
}

const TONE: Record<PillTone, { dot: string; text: string }> = {
  ok:      { dot: 'bg-green-400',           text: 'text-green-400' },
  pending: { dot: 'bg-yellow-400',          text: 'text-yellow-400' },
  off:     { dot: 'bg-zinc-500',            text: 'text-muted-foreground' },
  bad:     { dot: 'bg-red-400',             text: 'text-red-400' },
}

function walletPill(r: VenueReadiness): DiagnosticPill {
  if (r.walletConnected) return { label: 'Connected', tone: 'ok' }
  return r.status === 'error' ? { label: 'Error', tone: 'bad' } : { label: 'Not connected', tone: 'off' }
}
function signerPill(r: VenueReadiness): DiagnosticPill {
  if (!r.walletConnected) return { label: '—', tone: 'off' }
  return r.signerReady ? { label: 'Ready', tone: 'ok' } : { label: 'Missing', tone: 'pending' }
}
function agentPill(r: VenueReadiness): DiagnosticPill {
  if (!r.walletConnected) return { label: '—', tone: 'off' }
  if (r.agentStatus === 'restoring') return { label: 'Restoring', tone: 'pending', loading: true }
  if (r.agentStatus === 'authorizing') return { label: 'Authorizing', tone: 'pending', loading: true }
  if (r.agentStatus === 'disconnecting') return { label: 'Disconnecting', tone: 'pending', loading: true }
  if (r.agentStatus === 'error') return { label: 'Error', tone: 'bad' }
  return r.agentReady ? { label: 'Ready', tone: 'ok' } : { label: 'Required', tone: 'pending' }
}
// Overall aggregate → header color + copy. Kept local so the aggregate
// hook can stay purely data.
function aggregateTone(label: 'Ready' | 'Needs attention' | 'Not connected'): PillTone {
  if (label === 'Ready') return 'ok'
  if (label === 'Needs attention') return 'pending'
  return 'off'
}

export function ConnectAccounts({ open, onConnectionChange, onClose }: Props) {
  const {
    pacifica,
    hyperliquid,
    aster,
    phoenix,
    aggregate,
    refreshBalances,
  } = useVenueReadiness()
  const tradingAgents = useTradingAgents()
  const telegramLink = useTelegramLink()
  const { state: liveExecution } = useLiveExecution()
  const { positions } = useLivePositions()
  const agentChangeBlocked = !['idle', 'open', 'degraded', 'aborted', 'failed'].includes(liveExecution.phase)
  const agentChangeBlockedRef = useRef(agentChangeBlocked)

  useEffect(() => {
    agentChangeBlockedRef.current = agentChangeBlocked
  }, [agentChangeBlocked])

  // Opening the panel is a user intent to see fresh state — nudge a refresh.
  // (Background poll is deliberately slow at 30s.)
  useEffect(() => {
    if (open) refreshBalances().catch(() => {})
  }, [open, refreshBalances])

  const connection = useConnectVenue()
  const [pendingDisconnect, setPendingDisconnect] = useState<VenueId | null>(null)

  useEffect(() => {
    // Preserve existing parent contract: this counts fully-ready venues.
    onConnectionChange?.(aggregate.readyCount)
  }, [aggregate.readyCount, onConnectionChange])

  const disconnectVenue = async (venue: VenueId) => {
    try {
      await tradingAgents.disconnectVenue(venue)
    } catch {
      // The authorization UI owns the failure state.
    }
  }

  const handleDisconnect = (venueId: VenueId) => {
    if (hasActiveLiveExposure(positions.filter((position) => position.venue_a === venueId || position.venue_b === venueId))) {
      setPendingDisconnect(venueId)
      return
    }
    void disconnectVenue(venueId)
  }

  const confirmDisconnect = () => {
    const venue = pendingDisconnect
    setPendingDisconnect(null)
    if (venue && !agentChangeBlockedRef.current) void disconnectVenue(venue)
  }

  const handleAuthorize = async (venue: VenueId) => {
    try {
      await tradingAgents.authorize(venue)
      trackAnalytics('agent_authorized', { venue })
    } catch {
      // The authorization UI owns the failure state.
    }
  }

  const getReadiness = (venueId: string): VenueReadiness | null => {
    if (venueId === 'pacifica') return pacifica
    if (venueId === 'hyperliquid') return hyperliquid
    if (venueId === 'aster') return aster
    if (venueId === 'phoenix') return phoenix
    return null
  }

  const summaryTone = TONE[aggregateTone(aggregate.statusLabel)]
  const showTelegram = pacifica.walletConnected
    && hyperliquid.walletConnected
    && !!pacifica.address
    && !!hyperliquid.address

  const handleConnectTelegram = () => {
    if (!pacifica.address || !hyperliquid.address) return
    telegramLink.connect(pacifica.address, hyperliquid.address)
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
          <p className="text-[10px] text-muted-foreground/60 mt-0.5">
            {aggregate.readyCount}/{aggregate.totalCount} active venues ready
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
      </div>

      {/* Venue list */}
      <div className="flex-1 overflow-auto min-h-0 px-3 py-3">
        <p className="text-[10px] text-muted-foreground/50 uppercase tracking-wider font-medium mb-2 px-1">Venues</p>
        <div className="flex flex-col gap-2">
          {VENUES.map((venue) => {
            const readiness = getReadiness(venue.id)
            const isReady = readiness?.status === 'ready'
            const isErr = readiness?.status === 'error'
            const sharedSolanaOwner = !!pacifica.address && pacifica.address === phoenix.address
            const disconnectBlocked = agentChangeBlocked || readiness?.agentStatus === 'restoring'
              || (sharedSolanaOwner && venue.id === 'pacifica' && ['restoring', 'authorizing', 'disconnecting'].includes(tradingAgents.phoenix.status))
              || (sharedSolanaOwner && venue.id === 'phoenix' && ['restoring', 'authorizing', 'disconnecting'].includes(tradingAgents.pacifica.status))
              || readiness?.agentStatus === 'authorizing' || readiness?.agentStatus === 'disconnecting'
            const authorizeBlocked = agentChangeBlocked || disconnectBlocked || !readiness?.signerReady

            return (
              <div key={venue.id} role="group" aria-label={`${venue.name} account`}
                className={`rounded-lg border px-3 py-3 ${
                  isReady
                    ? 'border-green-500/20 bg-green-500/[0.03]'
                    : isErr
                      ? 'border-red-500/25 bg-red-500/[0.03]'
                      : 'border-border bg-white/[0.02]'
                }`}
              >
                {/* Top row: logo + name + address */}
                <div className="flex items-center gap-2.5 mb-2">
                  <div className="size-8 rounded-md bg-white/[0.04] border border-border flex items-center justify-center shrink-0 overflow-hidden">
                    {venue.logo ? (
                      <img src={venue.logo} alt={venue.name} className={cn('object-contain', venue.id === 'phoenix' ? 'size-6' : 'size-full')} />
                    ) : (
                      <span className="text-xs font-bold text-muted-foreground">{venue.name[0]}</span>
                    )}
                  </div>
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-1.5">
                      <span className="text-xs font-semibold text-foreground">{venue.name}</span>
                      {venue.preview && <span className="text-[9px] px-1 py-px rounded bg-amber-500/10 text-amber-300/80 font-medium">Preview</span>}
                    </div>
                    {readiness?.shortAddress && (
                      <p className="text-[10px] text-muted-foreground/70 font-mono leading-snug mt-0.5 truncate">{readiness.shortAddress}</p>
                    )}
                  </div>
                </div>

                {/* Diagnostics */}
                {readiness && (
                  <div className="mt-2 mb-2 rounded border border-border/60 bg-white/[0.02] px-2 py-2 flex flex-col gap-1">
                    <DiagRow label="Wallet" pill={walletPill(readiness)} />
                    <DiagRow label="Owner signer" pill={signerPill(readiness)} />
                    <DiagRow label="Authorization" pill={agentPill(readiness)} />
                    {venue.preview ? (
                      <DiagRow label="Live execution" pill={{ label: 'Not enabled', tone: 'off' }} />
                    ) : (
                      <AccountBalance readiness={readiness} />
                    )}
                  </div>
                )}

                {/* Action */}
                {venue.id === 'phoenix' ? (
                  <PhoenixAuthorization key={tradingAgents.phoenix.ownerAddress ?? 'no-owner'}
                    disabled={agentChangeBlocked || disconnectBlocked}
                    hasActiveExposure={hasActiveLiveExposure(positions.filter((position) => position.venue_a === 'phoenix' || position.venue_b === 'phoenix'))}
                    onConnect={() => connection.connect('phoenix')}
                    onDisconnect={() => handleDisconnect('phoenix')} />
                ) : <div className="flex items-center justify-end">
                  {readiness?.walletConnected ? (
                    <div className="flex items-center gap-1.5 flex-wrap justify-end">
                      {!readiness.signerReady && <button onClick={() => connection.connect(venue.id)} disabled={agentChangeBlocked || disconnectBlocked} className={accountActionStyles.primary}>Connect</button>}
                      {!readiness.agentReady && (
                        <button
                          onClick={() => handleAuthorize(venue.id as VenueId)}
                          disabled={authorizeBlocked}
                          className={accountActionStyles.primary}
                        >
                          {readiness.agentStatus === 'restoring'
                            ? 'Restoring authorization...'
                            : readiness.agentStatus === 'authorizing'
                            ? authorizationLabel(venue.id, tradingAgents.aster.authorizationStep)
                            : readiness.agentStatus === 'error' ? `Reauthorize ${venue.name}` : `Authorize ${venue.name}`}
                        </button>
                      )}
                      {readiness.agentReady && (
                        <>
                          <button
                            onClick={() => handleAuthorize(venue.id as VenueId)}
                            disabled={authorizeBlocked}
                            className={accountActionStyles.secondary}
                          >
                            Reauthorize
                          </button>
                        </>
                      )}
                      <button
                        onClick={() => handleDisconnect(venue.id)}
                        disabled={disconnectBlocked || (venue.id === 'pacifica' && !readiness.signerReady && readiness.agentStatus !== 'missing')}
                        className={accountActionStyles.secondary}
                      >
                        Disconnect
                      </button>
                    </div>
                  ) : (
                    <button
                      onClick={() => connection.connect(venue.id)}
                      disabled={agentChangeBlocked}
                      className={accountActionStyles.primary}
                    >
                      Connect
                    </button>
                  )}
                </div>}
                {connection.feedback?.venue === venue.id && <p className="mt-1.5 text-[10px] text-muted-foreground">{connection.feedback.text}</p>}

              </div>
            )
          })}

          {showTelegram && (
            <div className="py-1 animate-in fade-in-0 duration-200">
              <div className="px-1 mb-2">
                <p className="text-[10px] text-muted-foreground/50 uppercase tracking-wider font-medium">
                  Telegram
                </p>
                <p className="text-[10px] text-muted-foreground/70 mt-1 leading-relaxed">
                  View opportunities and live positions from Telegram.
                </p>
              </div>
              <button
                onClick={handleConnectTelegram}
                disabled={telegramLink.creating}
                className="w-full h-10 rounded-lg border border-[#229ED9]/25 bg-[#229ED9]/10 hover:border-[#229ED9]/40 hover:bg-[#229ED9]/15 disabled:opacity-70 disabled:cursor-wait text-[#8bd3f5] flex items-center justify-center gap-2 text-xs font-semibold transition-colors"
              >
                <img src={telegramLogo} alt="" className="size-5" />
                {telegramLink.creating ? 'Opening Telegram...' : 'Connect Telegram'}
              </button>
              {telegramLink.error && (
                <p className="text-[10px] text-red-400 mt-1.5 px-1">{telegramLink.error}</p>
              )}
            </div>
          )}

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
            Authorization keys stay encrypted in this browser profile.
          </p>
        </div>
      </div>

      {connection.picker && (
        <EvmWalletPicker
          venueName={connection.picker.venue === 'aster' ? 'Aster' : 'Hyperliquid'}
          connectors={connection.connectors}
          onPick={connection.pickConnector}
          onClose={connection.closePicker}
        />
      )}

      <Dialog open={!!connection.choice} onOpenChange={(open) => { if (!open) connection.closeChoice() }}>
        <DialogContent className="sm:max-w-sm">
          <DialogHeader>
            <DialogTitle>Connect {connection.choice ? venueMetadata(connection.choice.venue).label : 'account'}</DialogTitle>
            <DialogDescription>Choose which wallet to use for this venue.</DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" disabled={agentChangeBlocked} onClick={() => void connection.useAnother()}>Use another wallet</Button>
            <Button disabled={agentChangeBlocked} onClick={connection.useConnected}>Use {connection.choice?.address.slice(0, 6)}…{connection.choice?.address.slice(-4)}</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={pendingDisconnect !== null} onOpenChange={(nextOpen) => {
        if (!nextOpen) setPendingDisconnect(null)
      }}>
        <DialogContent className="sm:max-w-sm">
          <DialogHeader>
            <DialogTitle>Disconnect {pendingDisconnect ? venueMetadata(pendingDisconnect).label : 'account'}?</DialogTitle>
            <DialogDescription>
              Orbital will stop managing this venue's open positions. Reconnect and authorize again, or close them on the venue.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" size="sm" onClick={() => setPendingDisconnect(null)}>
              Cancel
            </Button>
            <Button size="sm" disabled={agentChangeBlocked} onClick={confirmDisconnect}>
              Disconnect
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}

function authorizationLabel(venue: VenueId, step?: 1 | 2 | 3): string {
  if (venue !== 'aster' || !step) return 'Authorizing…'
  if (step === 1) return 'Approving builder code…'
  if (step === 2) return 'Approving read-only agent…'
  return 'Approving execution agent…'
}

// Centered modal for choosing which EVM wallet to connect. Uses the EIP-6963
// entries wagmi's useConnect exposes; each installed wallet is a distinct
// row so the user is never silently routed to the window.ethereum default.
// Suggested wallets shown when the user has none installed. Deep-linked to
// each wallet's official download page so the empty state is actionable.
const SUGGESTED_EVM_WALLETS: { name: string; url: string; blurb: string }[] = [
  { name: 'MetaMask', url: 'https://metamask.io/download', blurb: 'Most widely supported' },
  { name: 'Rabby',    url: 'https://rabby.io/',            blurb: 'Made for power users' },
  { name: 'Rainbow',  url: 'https://rainbow.me/download',  blurb: 'Simple, mobile-first' },
]

function EvmWalletPicker({
  venueName,
  connectors,
  onPick,
  onClose,
}: {
  venueName: string
  connectors: readonly { uid: string; name: string; icon?: string }[]
  onPick: (uid: string) => void
  onClose: () => void
}) {
  const hasAny = connectors.length > 0

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-[2px]"
      onClick={onClose}
    >
      <div
        onClick={(e) => e.stopPropagation()}
        className="w-[380px] max-w-[90vw] rounded-lg border border-border bg-card shadow-2xl"
      >
        {/* Header */}
        <div className="flex items-start justify-between px-4 py-3 border-b border-border">
          <div className="min-w-0">
            <h3 className="text-sm font-semibold text-foreground">Connect {venueName}</h3>
            <p className="text-[11px] text-muted-foreground/80 mt-0.5">
              Pick an EVM wallet installed in your browser.
            </p>
          </div>
          <button
            onClick={onClose}
            className="text-muted-foreground hover:text-foreground size-6 flex items-center justify-center rounded hover:bg-white/[0.06] transition-colors shrink-0 ml-2"
          >
            <svg width="14" height="14" viewBox="0 0 16 16" fill="none">
              <path d="M4 4l8 8M12 4l-8 8" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" />
            </svg>
          </button>
        </div>

        {/* Body */}
        <div className="p-3 flex flex-col gap-2">
          {hasAny ? (
            <>
              <p className="text-[10px] uppercase tracking-wider text-muted-foreground/60 px-1">
                Detected wallets
              </p>
              <div className="flex flex-col gap-1">
                {connectors.map((c) => (
                  <button
                    key={c.uid}
                    onClick={() => onPick(c.uid)}
                    className="flex items-center gap-3 px-3 py-2 rounded text-sm font-medium bg-white/[0.03] hover:bg-white/[0.08] text-foreground transition-colors group"
                  >
                    {c.icon ? (
                      <img src={c.icon} alt="" className="size-7 rounded" />
                    ) : (
                      <div className="size-7 rounded bg-white/[0.08] flex items-center justify-center text-[11px] font-bold text-muted-foreground">
                        {c.name[0]}
                      </div>
                    )}
                    <span className="truncate flex-1 text-left">{c.name}</span>
                    <svg width="12" height="12" viewBox="0 0 16 16" fill="none" className="text-muted-foreground/40 group-hover:text-muted-foreground transition-colors">
                      <path d="M6 3l5 5-5 5" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />
                    </svg>
                  </button>
                ))}
              </div>
            </>
          ) : (
            <div className="flex flex-col gap-3">
              <div className="rounded border border-yellow-500/20 bg-yellow-500/[0.04] px-3 py-2">
                <p className="text-[11px] text-yellow-200/80 font-medium">No EVM wallet detected</p>
                <p className="text-[10px] text-muted-foreground/70 mt-1 leading-relaxed">
                   Install an EVM browser wallet, then refresh this page. Solana-only wallets
                   cannot connect to {venueName}.
                </p>
              </div>
              <p className="text-[10px] uppercase tracking-wider text-muted-foreground/60 px-1">
                Suggested
              </p>
              <div className="flex flex-col gap-1">
                {SUGGESTED_EVM_WALLETS.map((w) => (
                  <a
                    key={w.name}
                    href={w.url}
                    target="_blank"
                    rel="noopener noreferrer"
                    className="flex items-center justify-between px-3 py-2 rounded text-sm bg-white/[0.03] hover:bg-white/[0.08] text-foreground transition-colors"
                  >
                    <div className="min-w-0">
                      <p className="font-medium">{w.name}</p>
                      <p className="text-[10px] text-muted-foreground/70">{w.blurb}</p>
                    </div>
                    <span className="text-[10px] text-blue-400 shrink-0 ml-2">Install ↗</span>
                  </a>
                ))}
              </div>
            </div>
          )}
        </div>

        {/* Footer explainer */}
        <div className="px-4 py-2.5 border-t border-border">
          <p className="text-[10px] text-muted-foreground/60 leading-relaxed">
            Your wallet approves the connection and venue authorization. Trading agent keys stay
            in this browser session; Orbital never sees your seed phrase.
          </p>
        </div>
      </div>
    </div>
  )
}

function DiagRow({ label, pill }: { label: string; pill: DiagnosticPill }) {
  const t = TONE[pill.tone]
  const textColor = pill.loading ? 'text-cyan-400' : t.text
  return (
    <div className="flex items-center justify-between text-[10px]">
      <span className="text-muted-foreground">{label}</span>
      <div className="flex items-center gap-1.5">
        {pill.loading ? (
          <div
            className="size-2.5 animate-spin rounded-full border border-slate-500/40 border-t-cyan-400"
            aria-hidden="true"
          />
        ) : (
          <div className={`size-1.5 rounded-full ${t.dot}`} />
        )}
        <span className={`font-medium ${textColor}`}>{pill.label}</span>
      </div>
    </div>
  )
}

// Suppress unused-VenueId warning if consumers of this file don't import it.
export type { VenueId }
