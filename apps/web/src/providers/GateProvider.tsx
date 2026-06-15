import { useEffect, useState, type ReactNode } from 'react'
import { apiFetch } from '@/lib/api'
import { Gate } from '@/components/Gate'

// Centralizes gate detection: a single probe to /api/v1/health (which the API
// middleware always allows through). If the probe succeeds the visitor either
// has a valid __beta cookie or the API is in dev no-auth mode — either way,
// render the app. If it fails, render the Gate page so the user can redeem
// an invite. Re-probes on tab focus so a cookie redeemed in another tab is
// picked up without a manual refresh.

type Status = 'checking' | 'open' | 'gated'

export function GateProvider({ children }: { children: ReactNode }) {
  const [status, setStatus] = useState<Status>('checking')

  useEffect(() => {
    let cancelled = false

    async function probe() {
      try {
        const resp = await apiFetch('/api/v1/health')
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
