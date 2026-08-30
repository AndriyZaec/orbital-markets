import { useEffect, useRef, useState, type FormEvent } from 'react'
import { OrbitalField } from './OrbitalField'
import { trackAnalytics } from './analytics'

type Scene = 'hero' | 'access' | 'submitting' | 'received'

const APP_URL = import.meta.env.VITE_APP_URL || 'https://app.orbitalmarkets.xyz'
const WAITLIST_ENDPOINT = import.meta.env.VITE_WAITLIST_ENDPOINT || '/api/waitlist'
const METRICS_ENDPOINT = import.meta.env.VITE_METRICS_ENDPOINT || 'https://orbital-markets-funding.fly.dev/api/v1/public/metrics'

function Brand() {
  return (
    <div className="brand">
      <span className="brand-mark"><i /><i /><b /></span>
      <span>Orbital Markets</span>
    </div>
  )
}

// Rocket path derived from Lucide (MIT), kept inline to avoid an icon dependency.
function RocketIcon() {
  return (
    <svg className="action-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M4.5 16.5c-1.5 1.26-2 5-2 5s3.74-.5 5-2c.71-.84.7-2.13-.09-2.91a2.18 2.18 0 0 0-2.91-.09z" />
      <path d="m12 15-3-3a22 22 0 0 1 2-3.95A12.87 12.87 0 0 1 22 2c0 2.72-.78 7.5-6.05 11A22.35 22.35 0 0 1 12 15z" />
      <path d="M9 12H4s.55-3.03 2-4c1.62-1.08 5 0 5 0M12 15v5s3.03-.55 4-2c1.08-1.62 0-5 0-5" />
      <circle cx="16" cy="8" r="1" />
    </svg>
  )
}

export function LandingPage() {
  const [scene, setScene] = useState<Scene>('hero')
  const [paused, setPaused] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [totalVolume, setTotalVolume] = useState<string | null>(null)
  const heroButtonRef = useRef<HTMLButtonElement>(null)
  const emailRef = useRef<HTMLInputElement>(null)
  const initializedRef = useRef(false)
  const landingViewTrackedRef = useRef(false)
  const requestRef = useRef<AbortController | null>(null)

  useEffect(() => {
    if (landingViewTrackedRef.current) return
    landingViewTrackedRef.current = true
    trackAnalytics('landing_view', { source: 'landing' })
  }, [])

  useEffect(() => {
    const controller = new AbortController()
    fetch(METRICS_ENDPOINT, { signal: controller.signal })
      .then(async (response) => {
        if (!response.ok) throw new Error(`Metrics request failed with ${response.status}`)
        return response.json() as Promise<{ total_volume?: string }>
      })
      .then((metrics) => {
        const volume = Number(metrics.total_volume)
        if (Number.isFinite(volume) && volume >= 0) setTotalVolume(formatUsd(volume))
      })
      .catch(() => undefined)
    return () => controller.abort()
  }, [])

  useEffect(() => {
    if (!initializedRef.current) {
      initializedRef.current = true
      return
    }
    if (scene === 'access') emailRef.current?.focus()
    if (scene === 'hero') heroButtonRef.current?.focus({ preventScroll: true })
  }, [scene])

  useEffect(() => {
    function closeOnEscape(event: KeyboardEvent) {
      if (event.key === 'Escape' && scene !== 'hero') closeAccess()
    }
    window.addEventListener('keydown', closeOnEscape)
    return () => window.removeEventListener('keydown', closeOnEscape)
  }, [scene])

  useEffect(() => () => requestRef.current?.abort(), [])

  function openAccess() {
    setError(null)
    setScene('access')
    trackAnalytics('access_cta_clicked')
  }

  function closeAccess() {
    requestRef.current?.abort()
    requestRef.current = null
    setError(null)
    setScene('hero')
  }

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (scene === 'submitting') return
    setError(null)
    setScene('submitting')

    const form = new FormData(event.currentTarget)
    const controller = new AbortController()
    requestRef.current = controller
    try {
      const response = await fetch(WAITLIST_ENDPOINT, {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({
          email: String(form.get('email') ?? ''),
          profile: String(form.get('profile') ?? ''),
          monthly_volume: String(form.get('monthly_volume') ?? ''),
          source: 'landing',
        }),
        signal: controller.signal,
      })
      const contentType = response.headers.get('content-type') ?? ''
      if (!response.ok || !contentType.includes('application/json')) {
        throw new Error(`Waitlist request failed with ${response.status}`)
      }
      const result: unknown = await response.json()
      if (!result || typeof result !== 'object' || !('ok' in result) || result.ok !== true) {
        throw new Error('Waitlist request returned an invalid response')
      }
      trackAnalytics('waitlist_submitted', {
        profile: String(form.get('profile') ?? ''),
        monthly_volume: String(form.get('monthly_volume') ?? ''),
      })
      requestRef.current = null
      setScene('received')
    } catch (requestError) {
      if (requestError instanceof DOMException && requestError.name === 'AbortError') return
      requestRef.current = null
      setError('Could not submit your request. Please try again.')
      setScene('access')
    }
  }

  const accessVisible = scene !== 'hero'
  const submitting = scene === 'submitting'

  return (
    <main className={`landing scene-${scene}`}>
      <OrbitalField paused={paused} />
      <div className="vignette" />

      <header className="header">
        <Brand />
        <a href={APP_URL}>Enter app <span>-&gt;</span></a>
      </header>

      <section className="hero" aria-hidden={accessVisible} inert={accessVisible}>
        <p>Delta-neutral execution for perpetual markets</p>
        <h1>Trade the spread.<span>Not the market.</span></h1>
        <p>One non-custodial execution flow for hedged positions across perpetual venues, subject to execution, basis, and liquidation risk.</p>
        <button ref={heroButtonRef} type="button" onClick={openAccess}>Join Orbital <RocketIcon /></button>
      </section>

      <section className="access" aria-hidden={!accessVisible} inert={!accessVisible}>
        {scene === 'received' ? (
          <div className="received" aria-live="polite">
            <p>Beta</p>
            <h2>Request received.</h2>
            <span>We will be in touch as the next cohort opens.</span>
            <button type="button" onClick={() => setScene('hero')}>Back to signal</button>
          </div>
        ) : (
          <>
            <button className="back" type="button" onClick={closeAccess}>&lt;- Back</button>
            <div className="access-copy"><p>Beta</p><h2>Request access.</h2><span>For traders, teams, and researchers exploring a different way to trade perpetual markets.</span></div>
            <form className="form" onSubmit={submit} aria-busy={submitting}>
              <label><span>Email address</span><input ref={emailRef} name="email" type="email" autoComplete="email" required placeholder="you@somewhere.xyz" /></label>
              <div className="form-pair">
                <label><span>You are</span><select name="profile" required defaultValue=""><option value="" disabled>Select profile</option><option value="active_trader">Active perp trader</option><option value="occasional_trader">Occasional perp trader</option><option value="defi_user">DeFi user</option><option value="return_seeker">Exploring better returns</option><option value="trading_team">Trading team or market maker</option><option value="researching">Researching delta-neutral strategies</option></select></label>
                <label><span>Monthly perp volume</span><select name="monthly_volume" required defaultValue=""><option value="" disabled>Select range</option><option value="under_1k">Under $1k</option><option value="1k_10k">$1k - $10k</option><option value="10k_50k">$10k - $50k</option><option value="50k_100k">$50k - $100k</option><option value="100k_1m">$100k - $1m</option><option value="1m_plus">$1m+</option></select></label>
              </div>
              <button type="submit" disabled={submitting}>{submitting ? 'Submitting...' : 'Submit request'} {!submitting && <RocketIcon />}</button>
              {error && <p className="form-error" role="alert">{error}</p>}
            </form>
          </>
        )}
      </section>

      <footer className="footer">
        <div className="total-volume" aria-live="polite"><span>Total volume</span><strong>{totalVolume ?? '--'}</strong></div>
        <button type="button" onClick={() => setPaused((current) => !current)}>{paused ? 'Play field' : 'Pause field'}</button>
      </footer>
    </main>
  )
}

function formatUsd(value: number) {
  return new Intl.NumberFormat('en-US', {
    style: 'currency',
    currency: 'USD',
    notation: 'compact',
    maximumFractionDigits: 1,
  }).format(value)
}
