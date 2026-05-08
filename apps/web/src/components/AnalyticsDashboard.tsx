import { useAnalytics } from '@/hooks/useAnalytics'
import type { AssetRow, RiskTierRow, CloseReasonRow } from '@/hooks/useAnalytics'
import { Badge } from '@/components/ui/badge'
import { Progress } from '@/components/ui/progress'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'

function num(v: unknown): number {
  if (typeof v === 'number') return v
  if (typeof v === 'string') return parseFloat(v) || 0
  return 0
}

function fmtPnL(n: unknown) {
  const v = num(n)
  const sign = v >= 0 ? '+' : ''
  return sign + v.toFixed(4)
}

function fmtHours(h: unknown) {
  const v = num(h)
  if (v < 1) return `${(v * 60).toFixed(0)}m`
  return `${v.toFixed(1)}h`
}

function fmtPct(n: unknown) {
  const v = num(n)
  return `${(v * 100).toFixed(1)}%`
}

function pnlColor(n: unknown) {
  return num(n) >= 0 ? 'text-green-400' : 'text-red-400'
}

export function AnalyticsDashboard() {
  const { data, loading, error } = useAnalytics()

  if (loading) return <p className="text-muted-foreground text-sm px-5 py-8">Loading analytics...</p>
  if (error) return <p className="text-destructive text-sm px-5 py-8">Error: {error}</p>
  if (!data || data.summary.total_trades === 0) {
    return <p className="text-muted-foreground text-sm px-5 py-8">No paper trades yet. Execute some trades to see analytics.</p>
  }

  const { summary } = data

  return (
    <div className="px-5 py-5 flex flex-col gap-5">
      {/* Header */}
      <div className="flex items-center gap-2">
        <h2 className="text-base font-semibold text-foreground">Analytics</h2>
        <Badge variant="outline" className="text-yellow-400 border-yellow-400/30 text-[11px]">paper</Badge>
        <span className="text-xs text-muted-foreground">{summary.total_trades} trades</span>
      </div>

      {/* KPI cards */}
      <div className="grid grid-cols-4 gap-3">
        <KPICard
          title="Total P&L"
          value={fmtPnL(summary.pnl.total_pnl)}
          color={pnlColor(summary.pnl.total_pnl)}
          subtitle={`${fmtPnL(summary.pnl.realized_pnl)} realized`}
        />
        <KPICard
          title="Win Rate"
          value={num(summary.closed_trades) > 0 ? fmtPct(num(summary.profitable_trades) / num(summary.closed_trades)) : '—'}
          subtitle={`${summary.profitable_trades}W / ${summary.unprofitable_trades}L`}
        />
        <KPICard
          title="Avg Hold"
          value={fmtHours(summary.avg_hold_hours)}
          subtitle={`${summary.open_trades} open · ${summary.failed_trades} failed`}
        />
        <KPICard
          title="Break-Even"
          value={fmtPct(summary.break_even.reached_rate)}
          subtitle={`${summary.break_even.reached_count} reached`}
        />
      </div>

      {/* P&L Breakdown */}
      <div className="rounded-lg border border-border bg-card p-4">
        <p className="text-[11px] font-medium text-muted-foreground uppercase tracking-wider mb-3">P&L Breakdown</p>
        <div className="flex gap-8 items-end">
          <PnLItem label="Price P&L" value={summary.pnl.price_pnl} />
          <PnLItem label="Funding P&L" value={summary.pnl.funding_pnl} />
          <div className="h-8 w-px bg-border" />
          <PnLItem label="Total" value={summary.pnl.total_pnl} bold />
        </div>
      </div>

      {/* Break-even */}
      <div className="rounded-lg border border-border bg-card p-4">
        <p className="text-[11px] font-medium text-muted-foreground uppercase tracking-wider mb-3">Break-Even Analysis</p>
        <div className="flex items-center gap-4 mb-2">
          <div className="flex-1">
            <div className="flex justify-between text-[11px] text-muted-foreground mb-1">
              <span>Reached ({summary.break_even.reached_count})</span>
              <span>Not reached ({summary.break_even.not_reached_count})</span>
            </div>
            <Progress value={num(summary.break_even.reached_rate) * 100} className="h-1.5" />
          </div>
          <span className="text-sm font-mono font-medium w-12 text-right">
            {fmtPct(summary.break_even.reached_rate)}
          </span>
        </div>
        <p className="text-[11px] text-muted-foreground">
          Avg est. break-even: {fmtHours(summary.break_even.avg_estimated_break_even_hours)} · Avg hold: {fmtHours(summary.avg_hold_hours)}
        </p>
      </div>

      {/* By Asset */}
      {data.by_asset && data.by_asset.length > 0 && (
        <div className="rounded-lg border border-border bg-card overflow-hidden">
          <p className="text-[11px] font-medium text-muted-foreground uppercase tracking-wider px-4 pt-4 pb-3">By Asset</p>
          <AssetTable rows={data.by_asset} />
        </div>
      )}

      {/* By Risk Tier */}
      {data.by_risk_tier && data.by_risk_tier.length > 0 && (
        <div className="rounded-lg border border-border bg-card overflow-hidden">
          <p className="text-[11px] font-medium text-muted-foreground uppercase tracking-wider px-4 pt-4 pb-3">By Risk Tier</p>
          <RiskTierTable rows={data.by_risk_tier} />
        </div>
      )}

      {/* By Close Reason */}
      {data.by_close_reason && data.by_close_reason.length > 0 && (
        <div className="rounded-lg border border-border bg-card overflow-hidden">
          <p className="text-[11px] font-medium text-muted-foreground uppercase tracking-wider px-4 pt-4 pb-3">By Close Reason</p>
          <CloseReasonTable rows={data.by_close_reason} />
        </div>
      )}
    </div>
  )
}

function KPICard({ title, value, color, subtitle }: { title: string; value: string; color?: string; subtitle: string }) {
  return (
    <div className="rounded-lg border border-border bg-card px-4 py-3.5">
      <p className="text-[11px] text-muted-foreground mb-1">{title}</p>
      <p className={`text-xl font-mono font-semibold ${color ?? 'text-foreground'}`}>{value}</p>
      <p className="text-[11px] text-muted-foreground mt-1">{subtitle}</p>
    </div>
  )
}

function PnLItem({ label, value, bold }: { label: string; value: number; bold?: boolean }) {
  return (
    <div>
      <p className="text-[11px] text-muted-foreground">{label}</p>
      <p className={`font-mono ${bold ? 'text-lg font-semibold' : 'text-sm'} ${pnlColor(value)}`}>
        {fmtPnL(value)}
      </p>
    </div>
  )
}

function AssetTable({ rows }: { rows: AssetRow[] }) {
  return (
    <Table>
      <TableHeader>
        <TableRow className="border-border hover:bg-transparent">
          <TableHead className="text-muted-foreground font-medium text-xs uppercase tracking-wider">Asset</TableHead>
          <TableHead className="text-right text-muted-foreground font-medium text-xs uppercase tracking-wider">Trades</TableHead>
          <TableHead className="text-right text-muted-foreground font-medium text-xs uppercase tracking-wider">Price P&L</TableHead>
          <TableHead className="text-right text-muted-foreground font-medium text-xs uppercase tracking-wider">Funding P&L</TableHead>
          <TableHead className="text-right text-muted-foreground font-medium text-xs uppercase tracking-wider">Total P&L</TableHead>
          <TableHead className="text-right text-muted-foreground font-medium text-xs uppercase tracking-wider">Avg Hold</TableHead>
          <TableHead className="text-right text-muted-foreground font-medium text-xs uppercase tracking-wider">BE Rate</TableHead>
          <TableHead className="text-right text-muted-foreground font-medium text-xs uppercase tracking-wider">Win Rate</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {rows.map((r) => (
          <TableRow key={r.asset} className="border-border hover:bg-white/[0.02]">
            <TableCell className="font-medium text-foreground">{r.asset}</TableCell>
            <TableCell className="text-right text-sm text-muted-foreground">{r.total_trades}</TableCell>
            <TableCell className={`text-right font-mono text-sm ${pnlColor(r.total_price_pnl)}`}>{fmtPnL(r.total_price_pnl)}</TableCell>
            <TableCell className={`text-right font-mono text-sm ${pnlColor(r.total_funding_pnl)}`}>{fmtPnL(r.total_funding_pnl)}</TableCell>
            <TableCell className={`text-right font-mono text-sm ${pnlColor(r.total_pnl)}`}>{fmtPnL(r.total_pnl)}</TableCell>
            <TableCell className="text-right text-sm text-muted-foreground">{fmtHours(r.avg_hold_hours)}</TableCell>
            <TableCell className="text-right text-sm text-muted-foreground">
              {num(r.break_even_reached_count) + num(r.break_even_not_reached_count) > 0
                  ? `${num(r.break_even_reached_count)}/${num(r.break_even_reached_count) + num(r.break_even_not_reached_count)}`
                  : '—'}
            </TableCell>
            <TableCell className="text-right text-sm text-muted-foreground">
              {num(r.closed_trades) > 0 ? fmtPct(num(r.profitable_trades) / num(r.closed_trades)) : '—'}
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}

function RiskTierTable({ rows }: { rows: RiskTierRow[] }) {
  return (
    <Table>
      <TableHeader>
        <TableRow className="border-border hover:bg-transparent">
          <TableHead className="text-muted-foreground font-medium text-xs uppercase tracking-wider">Risk Tier</TableHead>
          <TableHead className="text-right text-muted-foreground font-medium text-xs uppercase tracking-wider">Trades</TableHead>
          <TableHead className="text-right text-muted-foreground font-medium text-xs uppercase tracking-wider">Total P&L</TableHead>
          <TableHead className="text-right text-muted-foreground font-medium text-xs uppercase tracking-wider">Avg Hold</TableHead>
          <TableHead className="text-right text-muted-foreground font-medium text-xs uppercase tracking-wider">BE Reached</TableHead>
          <TableHead className="text-right text-muted-foreground font-medium text-xs uppercase tracking-wider">Win Rate</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {rows.map((r) => (
          <TableRow key={r.risk_tier} className="border-border hover:bg-white/[0.02]">
            <TableCell className="font-medium capitalize text-foreground">{r.risk_tier}</TableCell>
            <TableCell className="text-right text-sm text-muted-foreground">{r.total_trades}</TableCell>
            <TableCell className={`text-right font-mono text-sm ${pnlColor(r.total_pnl)}`}>{fmtPnL(r.total_pnl)}</TableCell>
            <TableCell className="text-right text-sm text-muted-foreground">{fmtHours(r.avg_hold_hours)}</TableCell>
            <TableCell className="text-right text-sm text-muted-foreground">{r.break_even_reached_count}</TableCell>
            <TableCell className="text-right text-sm text-muted-foreground">
              {num(r.closed_trades) > 0 ? fmtPct(num(r.profitable_trades) / num(r.closed_trades)) : '—'}
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}

function CloseReasonTable({ rows }: { rows: CloseReasonRow[] }) {
  return (
    <Table>
      <TableHeader>
        <TableRow className="border-border hover:bg-transparent">
          <TableHead className="text-muted-foreground font-medium text-xs uppercase tracking-wider">Close Reason</TableHead>
          <TableHead className="text-right text-muted-foreground font-medium text-xs uppercase tracking-wider">Count</TableHead>
          <TableHead className="text-right text-muted-foreground font-medium text-xs uppercase tracking-wider">Realized P&L</TableHead>
          <TableHead className="text-right text-muted-foreground font-medium text-xs uppercase tracking-wider">Avg Hold</TableHead>
          <TableHead className="text-right text-muted-foreground font-medium text-xs uppercase tracking-wider">Profitable</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {rows.map((r) => (
          <TableRow key={r.close_reason} className="border-border hover:bg-white/[0.02]">
            <TableCell className="font-medium text-foreground">{r.close_reason}</TableCell>
            <TableCell className="text-right text-sm text-muted-foreground">{r.total_trades}</TableCell>
            <TableCell className={`text-right font-mono text-sm ${pnlColor(r.total_realized_pnl)}`}>{fmtPnL(r.total_realized_pnl)}</TableCell>
            <TableCell className="text-right text-sm text-muted-foreground">{fmtHours(r.avg_hold_hours)}</TableCell>
            <TableCell className="text-right text-sm text-muted-foreground">{r.profitable_trades}/{r.total_trades}</TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}
