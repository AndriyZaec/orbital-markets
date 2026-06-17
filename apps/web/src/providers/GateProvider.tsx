import { useEffect, useState, type ReactNode } from 'react'
import { apiFetch } from '@/lib/api'
import { Gate } from '@/components/Gate'

// Centralizes gate detection: a single probe to a gated endpoint
// (/api/v1/opportunities). 200 means the __beta cookie is valid (or dev
// no-auth mode); 404 means the auth middleware rejected the request, so we
// render the gate. /api/v1/health is unsuitable as a probe because it always
// returns 200 for CF / Fly health checks. Re-probes on tab focus so a cookie
// redeemed in another tab is picked up without a manual refresh.

type Status = 'checking' | 'open' | 'gated'

export function GateProvider({ children }: { children: ReactNode }) {
  const [status, setStatus] = useState<Status>('checking')

  useEffect(() => {
    let cancelled = false

    async function probe() {
      try {
        const resp = await apiFetch('/api/v1/opportunities')
        if (cancelled) return
        setStatus(resp.ok ? 'open' : 'gated')
      } catch {
        if (cancelled) return
        setStatus('gated')
      }
    }

    probe()
    const onFocus = () => probe()
    window.addEventListener('focus', onFocus)
    return () => {
      cancelled = true
      window.removeEventListener('focus', onFocus)
    }
  }, [])

  if (status === 'checking') {
    // Blank — no spinner, no flash of branded UI before gate decision.
    return <div className="min-h-screen bg-black" />
  }
  if (status === 'gated') {
    return <Gate />
  }
  return <>{children}</>
}
