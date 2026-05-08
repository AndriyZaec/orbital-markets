export function AnalyticsDashboard() {
  return (
    <div className="flex flex-col items-center justify-center h-full gap-3 text-center px-5">
      <div className="size-12 rounded-full bg-white/[0.04] flex items-center justify-center">
        <svg width="24" height="24" viewBox="0 0 24 24" fill="none" className="text-muted-foreground">
          <path d="M3 3v18h18" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"/>
          <path d="M7 14l4-4 4 4 5-5" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"/>
        </svg>
      </div>
      <p className="text-sm text-foreground font-medium">Work in Progress</p>
      <p className="text-xs text-muted-foreground/60">Analytics dashboard coming soon</p>
    </div>
  )
}
