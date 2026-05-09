const CAPABILITIES = [
  {
    title: 'Opportunity Discovery',
    desc: 'Normalized funding rate and basis data across venues. Detect spreads without building per-venue ingestion.',
  },
  {
    title: 'Edge Evaluation',
    desc: 'Annualized edge computation with historical persistence scoring. Know whether a spread is a spike or a pattern.',
  },
  {
    title: 'Liquidity-Aware Sizing',
    desc: 'Position sizing that accounts for available notional, slippage estimates, and venue-specific constraints.',
  },
  {
    title: 'Execution Plan Construction',
    desc: 'Concrete two-leg plans with expected entry, margin requirements, and simulation before submission.',
  },
  {
    title: 'Risk & Degradation Signals',
    desc: 'Hedge mismatch detection, liquidation distance monitoring, and broken-state escalation.',
  },
  {
    title: 'Monitoring & Analytics',
    desc: 'Position lifecycle tracking, funding accrual, basis drift, and PnL attribution across open positions.',
  },
]

const WORKFLOW_STEPS = [
  { label: 'Discover', desc: 'Query live spread opportunities across supported venue pairs' },
  { label: 'Evaluate', desc: 'Assess edge persistence, liquidity depth, and execution feasibility' },
  { label: 'Size', desc: 'Compute position size within margin and slippage constraints' },
  { label: 'Execute', desc: 'Submit execution plan with simulation-verified parameters' },
  { label: 'Monitor', desc: 'Track hedge integrity, funding accrual, and degradation signals' },
  { label: 'Rotate', desc: 'Close or rebalance based on edge decay or risk thresholds' },
]

const WHY_ITEMS = [
  { title: 'Less Infrastructure', desc: 'Skip building venue-specific data pipelines, order routing, and position tracking for every integration.' },
  { title: 'Safer Execution', desc: 'Simulation-first execution with slippage bounds, margin checks, and partial fill handling built in.' },
  { title: 'Hedge Integrity', desc: 'Continuous monitoring of spread positions with degradation detection — broken hedges are first-class states.' },
  { title: 'Cleaner Path to Scale', desc: 'A structured control layer that grows from paper execution to constrained live execution to delegated autonomous workflows.' },
]

const ROADMAP = [
  {
    phase: 'Today',
    dot: 'bg-green-400',
    items: [
      'Opportunity intelligence across Pacifica + Hyperliquid',
      'Execution plan construction with simulation',
      'Paper execution with full position lifecycle',
      'Monitoring, analytics, and degradation tracking',
    ],
  },
  {
    phase: 'Next',
    dot: 'bg-blue-400',
    items: [
      'Constrained live execution with guardrails',
      'Account-linked execution via venue delegation',
      'Policy-bounded agent workflows',
    ],
  },
  {
    phase: 'Later',
    dot: 'bg-purple-400',
    items: [
      'Delegated execution under user-defined risk limits',
      'Capital rotation and allocator workflows',
      'Multi-strategy orchestration',
    ],
  },
]

export function ForAgents() {
  return (
    <div className="max-w-4xl mx-auto px-5 py-10">
      {/* Hero */}
      <div className="mb-12">
        <h1 className="text-2xl font-bold text-foreground mb-2">For Agents</h1>
        <p className="text-base text-muted-foreground leading-relaxed max-w-2xl">
          Use Orbital as the execution and risk control layer for autonomous capital.
        </p>
      </div>

      {/* Workflow */}
      <div className="mb-12">
        <h2 className="text-sm font-semibold text-foreground uppercase tracking-wider mb-3">Agent Workflow</h2>
        <div className="rounded-lg border border-border bg-white/[0.02] px-5 py-4">
          <div className="flex flex-col">
            {WORKFLOW_STEPS.map((step, i) => (
              <div key={step.label} className="flex items-start gap-3 relative">
                {i < WORKFLOW_STEPS.length - 1 && (
                  <div className="absolute left-[3px] top-[14px] w-px h-[calc(100%)] bg-border" />
                )}
                <div className="size-2 rounded-full bg-blue-400 shrink-0 mt-1.5 z-10" />
                <div className={i < WORKFLOW_STEPS.length - 1 ? 'pb-4' : ''}>
                  <p className="text-xs font-semibold text-foreground">{step.label}</p>
                  <p className="text-[11px] text-muted-foreground/50 leading-relaxed mt-0.5">{step.desc}</p>
                </div>
              </div>
            ))}
          </div>
        </div>
      </div>

      {/* Why agents need Orbital */}
      <div className="mb-12">
        <h2 className="text-sm font-semibold text-foreground uppercase tracking-wider mb-3">Why Agents Need This</h2>
        <div className="rounded-lg border border-border bg-white/[0.02] px-5 py-4">
          <p className="text-sm text-muted-foreground leading-relaxed">
            Raw funding rate signals are not enough to run a spread strategy. Agents also need normalized venue data, execution-aware sizing, slippage and liquidity checks, hedge integrity rules, and continuous monitoring with degradation handling. Building this per-venue, per-strategy is expensive and fragile.
          </p>
          <p className="text-sm text-muted-foreground leading-relaxed mt-3">
            Orbital provides the structured control layer — agents decide capital allocation, Orbital handles everything between the decision and the result.
          </p>
        </div>
      </div>

      {/* Capabilities */}
      <div className="mb-12">
        <h2 className="text-sm font-semibold text-foreground uppercase tracking-wider mb-3">What Orbital Provides</h2>
        <div className="grid grid-cols-2 gap-3">
          {CAPABILITIES.map((c) => (
            <div key={c.title} className="rounded-lg border border-border bg-white/[0.02] px-4 py-3.5">
              <p className="text-sm font-medium text-foreground mb-1">{c.title}</p>
              <p className="text-[11px] text-muted-foreground/60 leading-relaxed">{c.desc}</p>
            </div>
          ))}
        </div>
      </div>

      {/* Why this matters */}
      <div className="mb-12">
        <h2 className="text-sm font-semibold text-foreground uppercase tracking-wider mb-3">Why This Matters</h2>
        <div className="grid grid-cols-2 gap-3">
          {WHY_ITEMS.map((item) => (
            <div key={item.title} className="rounded-lg border border-blue-500/10 bg-blue-500/[0.04] px-4 py-3.5">
              <p className="text-sm font-medium text-foreground mb-1">{item.title}</p>
              <p className="text-[11px] text-blue-300/50 leading-relaxed">{item.desc}</p>
            </div>
          ))}
        </div>
      </div>

      {/* Roadmap */}
      <div className="mb-12">
        <h2 className="text-sm font-semibold text-foreground uppercase tracking-wider mb-3">Roadmap</h2>
        <div className="grid grid-cols-3 gap-3">
          {ROADMAP.map((phase) => (
            <div key={phase.phase} className="rounded-lg border border-border bg-white/[0.02] px-4 py-4">
              <div className="flex items-center gap-2 mb-3">
                <div className={`size-2 rounded-full ${phase.dot}`} />
                <span className="text-sm font-semibold text-foreground">{phase.phase}</span>
              </div>
              <ul className="flex flex-col gap-2">
                {phase.items.map((item) => (
                  <li key={item} className="text-[11px] text-muted-foreground/60 leading-relaxed flex items-start gap-2">
                    <span className="text-muted-foreground/30 mt-px shrink-0">&#8226;</span>
                    {item}
                  </li>
                ))}
              </ul>
            </div>
          ))}
        </div>
      </div>

      {/* Footer CTA */}
      <div className="rounded-lg border border-blue-500/20 bg-blue-500/[0.06] px-6 py-5 text-center">
        <p className="text-base font-semibold text-foreground mb-1">Build agent workflows on top of Orbital</p>
        <p className="text-sm text-muted-foreground/60">
          Programmable carry execution with built-in risk controls and venue abstraction.
        </p>
      </div>
    </div>
  )
}
