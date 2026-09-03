import { useEffect, useState } from 'react'
import { clearAdminToken, getAdminToken, setAdminToken } from './api'
import { AuditPage } from './features/AuditPage'
import { AnalyticsPage } from './features/AnalyticsPage'
import { WaitlistPage } from './features/WaitlistPage'
import { WeeklyAPRPage } from './features/WeeklyAPRPage'

type AdminSection = 'overview' | 'weekly-apr' | 'waitlist' | 'users' | 'audit'

interface AdminIdentity {
  email: string
}

async function loadIdentity(): Promise<AdminIdentity> {
  const token = getAdminToken()
  const response = await fetch('/api/admin/v1/me', {
    credentials: 'same-origin',
    headers: token ? { Authorization: `Bearer ${token}` } : {},
  })
  if (!response.ok) throw new Error(response.status === 401 || response.status === 403 ? 'Enter a valid admin token.' : 'Unable to load admin identity.')
  return await response.json() as AdminIdentity
}

export function App() {
  const [section, setSection] = useState<AdminSection>('overview')
  const [identity, setIdentity] = useState<AdminIdentity | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [token, setToken] = useState('')
  const [submitting, setSubmitting] = useState(false)

  useEffect(() => {
    loadIdentity().then(setIdentity).catch((reason: unknown) => {
      setError(reason instanceof Error ? reason.message : 'Unable to authenticate.')
    })
  }, [])

  if (error) {
    return (
      <main className="auth-state">
        <div className="auth-card">
          <span className="eyebrow">Orbital Markets</span>
          <h1>Admin access required</h1>
          <p>{error}</p>
          <p className="muted">Enter the admin token. It is kept only for this browser session.</p>
          <form onSubmit={async (event) => {
            event.preventDefault()
            const nextToken = token.trim()
            if (!nextToken) return
            setSubmitting(true)
            setError(null)
            setAdminToken(nextToken)
            try {
              setIdentity(await loadIdentity())
            } catch (reason: unknown) {
              clearAdminToken()
              setError(reason instanceof Error ? reason.message : 'Unable to authenticate.')
            } finally {
              setSubmitting(false)
            }
          }}>
            <label className="auth-label" htmlFor="admin-token">Admin token</label>
            <input id="admin-token" className="auth-input" type="password" autoComplete="current-password" value={token} onChange={(event) => setToken(event.target.value)} />
            <button type="submit" disabled={submitting}>{submitting ? 'Checking...' : 'Continue'}</button>
          </form>
        </div>
      </main>
    )
  }

  if (!identity) {
    return <main className="auth-state"><p className="muted">Verifying operator identity...</p></main>
  }

  return (
    <div className="app-shell">
      <aside className="sidebar">
        <div className="brand"><span className="brand-mark">O</span><span>Orbital <small>ADMIN</small></span></div>
        <nav aria-label="Admin sections">
          <NavItem active={section === 'overview'} onClick={() => setSection('overview')} label="Overview" />
          <NavItem active={section === 'weekly-apr'} onClick={() => setSection('weekly-apr')} label="Weekly APR" />
          <NavItem active={section === 'waitlist'} onClick={() => setSection('waitlist')} label="Waitlist" />
          <NavItem active={section === 'users'} onClick={() => setSection('users')} label="Users" />
          <NavItem active={section === 'audit'} onClick={() => setSection('audit')} label="Audit log" />
        </nav>
        <div className="sidebar-footer"><span className="status-dot" /> Token protected</div>
      </aside>
      <main className="content">
        <header className="topbar">
          <div><span className="eyebrow">Beta operations</span><h1>{sectionTitle(section)}</h1></div>
          <div className="operator"><span className="status-dot" />{identity.email}</div>
        </header>
        <section className="page-content">{section === 'overview' ? <AnalyticsPage /> : section === 'weekly-apr' ? <WeeklyAPRPage /> : section === 'waitlist' ? <WaitlistPage key="waitlist" /> : section === 'users' ? <WaitlistPage key="users" mode="users" /> : <AuditPage />}</section>
      </main>
    </div>
  )
}

function NavItem({ active, onClick, label }: { active: boolean; onClick: () => void; label: string }) {
  return <button type="button" className={`nav-item${active ? ' active' : ''}`} onClick={onClick}>{label}</button>
}

function sectionTitle(section: AdminSection): string {
  return section === 'overview' ? 'Operations overview' : section === 'weekly-apr' ? 'Weekly APR' : section === 'waitlist' ? 'Waitlist' : section === 'users' ? 'Users' : 'Audit log'
}
