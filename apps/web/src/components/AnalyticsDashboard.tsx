import { useAnalytics } from '@/hooks/useAnalytics'
import type { AssetRow, RiskTierRow, CloseReasonRow } from '@/hooks/useAnalytics'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { Progress } from '@/components/ui/progress'
import { Separator } from '@/components/ui/separator'
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

  if (loading) return <p className="text-muted-foreground p-6">Loading analytics...</p>
  if (error) return <p className="text-destructive p-6">Error: {error}</p>
  if (!data || data.summary.total_trades === 0) {
    return <p className="text-muted-foreground p-6">No paper trades yet. Execute some trades to see analytics.</p>
  }

  const { summary } = data

  return (
    <div className="p-6 flex flex-col gap-6">
      {/* Mode badge */}
      <div className="flex items-center gap-2">
        <Badge variant="outline" className="text-yellow-400 border-yellow-400/30">paper mode</Badge>
        <span className="text-xs text-muted-foreground">{summary.total_trades} total trades</span>
      </div>

      {/* KPI cards */}
      <div className="grid grid-cols-4 gap-4">
        <KPICard
          title="Total P&L"
          value={fmtPnL(summary.pnl.total_pnl)}
          color={pnlColor(summary.pnl.total_pnl)}
          subtitle={`${fmtPnL(summary.pnl.realized_pnl)} realized · ${fmtPnL(summary.pnl.unrealized_pnl)} unrealized`}
        />
        <KPICard
          title="Win Rate"
          value={num(summary.closed_trades) > 0 ? fmtPct(num(summary.profitable_trades) / num(summary.closed_trades)) : '—'}
          subtitle={`${summary.profitable_trades}W / ${summary.unprofitable_trades}L of ${summary.closed_trades} closed`}
        />
        <KPICard
          title="Avg Hold Time"
          value={fmtHours(summary.avg_hold_hours)}
          subtitle={`${summary.open_trades} open · ${summary.failed_trades} failed`}
        />
        <KPICard
          title="Break-Even Rate"
          value={fmtPct(summary.break_even.reached_rate)}
          subtitle={`${summary.break_even.reached_count} reached · avg est. ${fmtHours(summary.break_even.avg_estimated_break_even_hours)}`}
        />
      </div>

      {/* P&L Breakdown */}
      <Card>
        <CardHeader className="pb-3">
          <CardTitle className="text-sm font-medium">P&L Breakdown</CardTitle>
        </CardHeader>
        <CardContent>
          <div className="flex gap-8">
            <PnLItem label="Price P&L" value={summary.pnl.price_pnl} />
            <PnLItem label="Funding P&L" value={summary.pnl.funding_pnl} />
            <Separator orientation="vertical" className="h-10" />
            <PnLItem label="Total" value={summary.pnl.total_pnl} bold />
          </div>
        </CardContent>
      </Card>

      {/* Break-even detail */}
      <Card>
        <CardHeader className="pb-3">
          <CardTitle className="text-sm font-medium">Break-Even Analysis</CardTitle>
        </CardHeader>
        <CardContent>
          <div className="flex items-center gap-4 mb-3">
            <div className="flex-1">
              <div className="flex justify-between text-xs text-muted-foreground mb-1">
                <span>Reached ({summary.break_even.reached_count})</span>
                <span>Not reached ({summary.break_even.not_reached_count})</span>
              </div>
              <Progress value={num(summary.break_even.reached_rate) * 100} className="h-2" />
            </div>
            <span className="text-sm font-mono font-medium w-12 text-right">
              {fmtPct(summary.break_even.reached_rate)}
            </span>
          </div>
          <p className="text-xs text-muted-foreground">
            Avg estimated break-even: {fmtHours(summary.break_even.avg_estimated_break_even_hours)} · Avg actual hold: {fmtHours(summary.avg_hold_hours)}
          </p>
        </CardContent>
      </Card>

      {/* By Asset */}
      {data.by_asset && data.by_asset.length > 0 && (
        <Card>
          <CardHeader className="pb-3">
            <CardTitle className="text-sm font-medium">By Asset</CardTitle>
          </CardHeader>
          <CardContent className="p-0">
            <AssetTable rows={data.by_asset} />
          </CardContent>
        </Card>
      )}

      {/* By Risk Tier */}
      {data.by_risk_tier && data.by_risk_tier.length > 0 && (
        <Card>
          <CardHeader className="pb-3">
            <CardTitle className="text-sm font-medium">By Risk Tier</CardTitle>
          </CardHeader>
          <CardContent className="p-0">
            <RiskTierTable rows={data.by_risk_tier} />
          </CardContent>
        </Card>
      )}

      {/* By Close Reason */}
      {data.by_close_reason && data.by_close_reason.length > 0 && (
        <Card>
          <CardHeader className="pb-3">
            <CardTitle className="text-sm font-medium">By Close Reason</CardTitle>
          </CardHeader>
          <CardContent className="p-0">
            <CloseReasonTable rows={data.by_close_reason} />
          </CardContent>
        </Card>
      )}
    </div>
  )
}

function KPICard({ title, value, color, subtitle }: { title: string; value: string; color?: string; subtitle: string }) {
  return (
    <Card>
      <CardContent className="pt-4 pb-3">
        <p className="text-xs text-muted-foreground mb-1">{title}</p>
        <p className={`text-2xl font-mono font-semibold ${color ?? ''}`}>{value}</p>
        <p className="text-xs text-muted-foreground mt-1">{subtitle}</p>
      </CardContent>
    </Card>
  )
}

function PnLItem({ label, value, bold }: { label: string; value: number; bold?: boolean }) {
  return (
    <div>
      <p className="text-xs text-muted-foreground">{label}</p>
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
        <TableRow>
          <TableHead>Asset</TableHead>
          <TableHead className="text-right">Trades</TableHead>
          <TableHead className="text-right">Price P&L</TableHead>
          <TableHead className="text-right">Funding P&L</TableHead>
          <TableHead className="text-right">Total P&L</TableHead>
          <TableHead className="text-right">Avg Hold</TableHead>
          <TableHead className="text-right">BE Reached</TableHead>
          <TableHead className="text-right">Win Rate</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {rows.map((r) => (
          <TableRow key={r.asset}>
            <TableCell className="font-medium">{r.asset}</TableCell>
            <TableCell className="text-right text-sm">{r.total_trades}</TableCell>
            <TableCell className={`text-right font-mono text-sm ${pnlColor(r.total_price_pnl)}`}>{fmtPnL(r.total_price_pnl)}</TableCell>
            <TableCell className={`text-right font-mono text-sm ${pnlColor(r.total_funding_pnl)}`}>{fmtPnL(r.total_funding_pnl)}</TableCell>
            <TableCell className={`text-right font-mono text-sm ${pnlColor(r.total_pnl)}`}>{fmtPnL(r.total_pnl)}</TableCell>
            <TableCell className="text-right text-sm">{fmtHours(r.avg_hold_hours)}</TableCell>
            <TableCell className="text-right text-sm">
              {num(r.break_even_reached_count) + num(r.break_even_not_reached_count) > 0
                  ? `${num(r.break_even_reached_count)}/${num(r.break_even_reached_count) + num(r.break_even_not_reached_count)}`
                  : '—'}
            </TableCell>
            <TableCell className="text-right text-sm">
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
        <TableRow>
          <TableHead>Risk Tier</TableHead>
          <TableHead className="text-right">Trades</TableHead>
          <TableHead className="text-right">Total P&L</TableHead>
          <TableHead className="text-right">Avg Hold</TableHead>
          <TableHead className="text-right">BE Reached</TableHead>
          <TableHead className="text-right">Win Rate</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {rows.map((r) => (
          <TableRow key={r.risk_tier}>
            <TableCell className="font-medium capitalize">{r.risk_tier}</TableCell>
            <TableCell className="text-right text-sm">{r.total_trades}</TableCell>
            <TableCell className={`text-right font-mono text-sm ${pnlColor(r.total_pnl)}`}>{fmtPnL(r.total_pnl)}</TableCell>
            <TableCell className="text-right text-sm">{fmtHours(r.avg_hold_hours)}</TableCell>
            <TableCell className="text-right text-sm">{r.break_even_reached_count}</TableCell>
            <TableCell className="text-right text-sm">
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
        <TableRow>
          <TableHead>Close Reason</TableHead>
          <TableHead className="text-right">Count</TableHead>
          <TableHead className="text-right">Realized P&L</TableHead>
          <TableHead className="text-right">Avg Hold</TableHead>
          <TableHead className="text-right">Profitable</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {rows.map((r) => (
          <TableRow key={r.close_reason}>
            <TableCell className="font-medium">{r.close_reason}</TableCell>
            <TableCell className="text-right text-sm">{r.total_trades}</TableCell>
            <TableCell className={`text-right font-mono text-sm ${pnlColor(r.total_realized_pnl)}`}>{fmtPnL(r.total_realized_pnl)}</TableCell>
            <TableCell className="text-right text-sm">{fmtHours(r.avg_hold_hours)}</TableCell>
            <TableCell className="text-right text-sm">{r.profitable_trades}/{r.total_trades}</TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}
