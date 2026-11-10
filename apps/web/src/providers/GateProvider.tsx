import { useEffect, useRef, useState, type ReactNode } from 'react'
import { apiFetch } from '@/lib/api'
import { Gate } from '@/components/Gate'
import { trackAnalytics } from '@/lib/analytics'

// Centralizes gate detection: a single probe to the gated access endpoint.
// 204 means the __beta cookie is valid (or dev
// no-auth mode); 404 means the auth middleware rejected the request, so we
// render the gate. /api/v1/health is unsuitable as a probe because it always
// returns 200 for CF / Fly health checks. Re-probes on tab focus so a cookie
// redeemed in another tab is picked up without a manual refresh.

type Status = 'checking' | 'open' | 'gated'

export function GateProvider({ children }: { children: ReactNode }) {
  const [status, setStatus] = useState<Status>('checking')
  const appOpenedTrackedRef = useRef(false)

  useEffect(() => {
    let cancelled = false
    let inflight: AbortController | null = null
    let retry = 0

    async function probe() {
      if (cancelled || inflight) return
      window.clearTimeout(retry)
      const controller = new AbortController()
      inflight = controller
      const deadline = window.setTimeout(() => controller.abort(), 5_000)
      let transient = false
      try {
        const resp = await apiFetch('/api/v1/access', { signal: controller.signal })
        if (cancelled) return
        if (resp.ok) setStatus('open')
        else if ([401, 403, 404].includes(resp.status)) setStatus('gated')
        else transient = true
        if (resp.ok && !appOpenedTrackedRef.current) {
          appOpenedTrackedRef.current = true
          trackAnalytics('app_opened')
        }
      } catch {
        transient = true
      } finally {
        window.clearTimeout(deadline)
        inflight = null
        // A failed network probe is not an access revocation. Preserve the
        // mounted account sessions and retry verification quietly.
        if (!cancelled && transient) retry = window.setTimeout(() => void probe(), 5_000)
      }
    }

    probe()
    const onFocus = () => probe()
    window.addEventListener('focus', onFocus)
    return () => {
      cancelled = true
      inflight?.abort()
      window.clearTimeout(retry)
      window.removeEventListener('focus', onFocus)
    }
  }, [])

  if (status === 'checking') {
    return (
      <div className="min-h-screen bg-black flex items-center justify-center" role="status" aria-live="polite">
        <div className="flex flex-col items-center gap-3 text-neutral-400">
          <span className="size-6 animate-spin rounded-full border border-neutral-700 border-t-cyan-400" aria-hidden="true" />
          <span className="text-[10px] font-medium uppercase tracking-[0.2em]">Loading Orbital</span>
        </div>
      </div>
    )
  }
  if (status === 'gated') {
    return <Gate />
  }
  return <>{children}</>
}
