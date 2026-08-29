import { useEffect, useState } from 'react'

type AdminSection = 'overview' | 'waitlist' | 'users' | 'audit'

interface AdminIdentity {
  email: string
}

async function loadIdentity(): Promise<AdminIdentity> {
  const response = await fetch('/api/admin/v1/me', { credentials: 'same-origin' })
  if (!response.ok) throw new Error(response.status === 401 || response.status === 403 ? 'Cloudflare Access authentication required.' : 'Unable to load admin identity.')
  return await response.json() as AdminIdentity
}

export function App() {
  const [section, setSection] = useState<AdminSection>('overview')
  const [identity, setIdentity] = useState<AdminIdentity | null>(null)
  const [error, setError] = useState<string | null>(null)

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
          <p className="muted">Authenticate through the Cloudflare Access policy for this hostname, then reload.</p>
          <button type="button" onClick={() => window.location.reload()}>Retry</button>
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
          <NavItem active={section === 'waitlist'} onClick={() => setSection('waitlist')} label="Waitlist" />
          <NavItem active={section === 'users'} onClick={() => setSection('users')} label="Users" />
          <NavItem active={section === 'audit'} onClick={() => setSection('audit')} label="Audit log" />
        </nav>
        <div className="sidebar-footer"><span className="status-dot" /> Access protected</div>
      </aside>
      <main className="content">
        <header className="topbar">
          <div><span className="eyebrow">Beta operations</span><h1>{sectionTitle(section)}</h1></div>
          <div className="operator"><span className="status-dot" />{identity.email}</div>
        </header>
        <section className="page-content"><SectionPlaceholder section={section} /></section>
      </main>
    </div>
  )
}

function NavItem({ active, onClick, label }: { active: boolean; onClick: () => void; label: string }) {
  return <button type="button" className={`nav-item${active ? ' active' : ''}`} onClick={onClick}>{label}</button>
}

function sectionTitle(section: AdminSection): string {
  return section === 'overview' ? 'Operations overview' : section === 'waitlist' ? 'Waitlist' : section === 'users' ? 'Users' : 'Audit log'
}

function SectionPlaceholder({ section }: { section: AdminSection }) {
  const copy = {
    overview: ['Admission pipeline', 'Review beta demand, invite delivery, and operational health in one place.'],
    waitlist: ['Waitlist review', 'Filter applicants and make explicit approval decisions.'],
    users: ['Beta users', 'Inspect invite history and the identity link for each approved applicant.'],
    audit: ['Operator activity', 'Every admin mutation will appear here with its verified Access identity.'],
  }[section]
  return <div className="empty-panel"><span className="panel-kicker">Coming online</span><h2>{copy[0]}</h2><p>{copy[1]}</p></div>
}
