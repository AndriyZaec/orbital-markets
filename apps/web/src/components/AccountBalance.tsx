import type { VenueReadiness } from '@/hooks/useVenueReadiness'
import { useLoadingFeedback } from '@/hooks/useLoadingFeedback'
import { Spinner } from '@/components/ui/spinner'
import { cn } from '@/lib/utils'
import { accountObservation } from '@/lib/account-observation'

function usd(value: number | null) {
  if (value === null || !Number.isFinite(value)) return '—'
  if (Math.abs(value) >= 1_000_000) return `$${(value / 1_000_000).toFixed(2)}M`
  if (Math.abs(value) >= 1_000) return `$${(value / 1_000).toFixed(2)}K`
  return `$${value.toFixed(2)}`
}

export function AccountBalance({ readiness: r }: { readiness: VenueReadiness }) {
  const key = JSON.stringify([r.venue, r.address, r.walletConnected])
  const display = accountObservation(r)
  const pending = r.walletConnected && !display
  const phase = useLoadingFeedback(pending, key)
  const updatedAt = display?.observed_at ? new Date(display.observed_at) : null
  const updateLabel = updatedAt && Number.isFinite(updatedAt.getTime())
    ? updatedAt.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' }) : '—'

  return <>
    {!display && <div className="flex items-center justify-between text-[10px]">
      <span className="text-muted-foreground">Balance</span>
      <span className={cn('inline-flex items-center gap-1.5', r.balanceReady && r.walletConnected ? 'text-green-400' : 'text-muted-foreground')}>
        {!r.walletConnected ? '—' : phase === 'unavailable' ? 'Unavailable'
          : phase === 'loading' ? <Spinner aria-label={`${r.label} balance updating`} /> : <span aria-label="Balance updating">—</span>}
      </span>
    </div>}
    {r.walletConnected && <>
      <div className="flex items-center justify-between text-[10px] pt-1 mt-0.5 border-t border-border/40">
        <span className="text-muted-foreground">Equity / Available</span>
        <span className={cn('font-mono', !r.balanceReady ? 'text-muted-foreground' : 'text-foreground')}>
          {usd(display?.equity ?? null)} / {usd(display?.available ?? null)}
        </span>
      </div>
      <div className="flex items-center justify-between text-[10px]">
        <span className="text-muted-foreground">Last update</span>
        <span className="font-mono text-muted-foreground">{updateLabel}</span>
      </div>
    </>}
  </>
}
