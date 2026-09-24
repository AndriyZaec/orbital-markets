import { Component, type ErrorInfo, type ReactNode } from 'react'

export class DeferredBoundary extends Component<
  { children: ReactNode; label: string },
  { failed: boolean }
> {
  state = { failed: false }

  static getDerivedStateFromError() {
    return { failed: true }
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error('Deferred UI failed to load', error, info)
  }

  render() {
    if (!this.state.failed) return this.props.children
    return (
      <div className="flex h-full min-h-24 w-full flex-col items-center justify-center gap-3 p-4 text-center">
        <p className="text-sm text-muted-foreground">{this.props.label} could not be loaded.</p>
        <button
          type="button"
          className="rounded border border-border px-3 py-1.5 text-xs text-foreground hover:bg-white/[0.06]"
          onClick={() => window.location.reload()}
        >
          Reload
        </button>
      </div>
    )
  }
}
