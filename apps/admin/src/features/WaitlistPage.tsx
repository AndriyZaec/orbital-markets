import { useEffect, useState } from 'react'
import {
  bulkTransition,
  getWaitlistDetail,
  issueInvite,
  listWaitlist,
  resendInvite,
  revokeInvite,
  transitionWaitlist,
  type WaitlistDetail,
  type WaitlistEntry,
} from '../api'

type StatusFilter = 'all' | WaitlistEntry['status']

export function WaitlistPage({ mode = 'waitlist' }: { mode?: 'waitlist' | 'users' }) {
  const [entries, setEntries] = useState<WaitlistEntry[]>([])
  const [status, setStatus] = useState<StatusFilter>(mode === 'users' ? 'invited' : 'pending')
  const [profile, setProfile] = useState('')
  const [volume, setVolume] = useState('')
  const [query, setQuery] = useState('')
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [detail, setDetail] = useState<WaitlistDetail | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [actionError, setActionError] = useState<string | null>(null)
  const [refresh, setRefresh] = useState(0)

  useEffect(() => {
    const controller = new AbortController()
    const params = new URLSearchParams({ limit: '100' })
    if (status !== 'all') params.set('status', status)
    if (profile) params.set('profile', profile)
    if (volume) params.set('monthly_volume', volume)
    if (query.trim()) params.set('q', query.trim())
    setLoading(true)
    listWaitlist(params, controller.signal)
      .then((response) => { setEntries(response.items); setSelected(new Set()); setError(null) })
      .catch((reason: unknown) => { if (!controller.signal.aborted) setError(reason instanceof Error ? reason.message : 'Unable to load waitlist.') })
      .finally(() => { if (!controller.signal.aborted) setLoading(false) })
    return () => controller.abort()
  }, [status, profile, volume, query, refresh])

  async function applyTransition(ids: string[], transition: 'approve' | 'reject') {
    if (ids.length === 0) return
    const verb = transition === 'approve' ? 'approve' : 'reject'
    if (!window.confirm(`Confirm ${verb} for ${ids.length} selected ${ids.length === 1 ? 'applicant' : 'applicants'}?`)) return
    setActionError(null)
    try {
      if (ids.length === 1) await transitionWaitlist(ids[0], transition)
      else await bulkTransition(ids, transition)
      setRefresh((value) => value + 1)
      if (detail && ids.includes(detail.entry.id)) setDetail(null)
    } catch (reason: unknown) {
      setActionError(reason instanceof Error ? reason.message : 'The waitlist action failed.')
    }
  }

  async function applyInviteAction(action: 'issue' | 'resend' | 'revoke', id: string) {
    const labels = { issue: 'issue', resend: 'resend', revoke: 'disable' }
    if (!window.confirm(`Confirm ${labels[action]} invite?`)) return
    setActionError(null)
    try {
      if (action === 'issue') await issueInvite(id)
      if (action === 'resend') await resendInvite(id)
      if (action === 'revoke') await revokeInvite(id)
      const next = await getWaitlistDetail(detail?.entry.id ?? id)
      setDetail(next)
      setRefresh((value) => value + 1)
    } catch (reason: unknown) {
      setActionError(reason instanceof Error ? reason.message : 'The invite action failed.')
    }
  }

  function toggle(id: string) {
    setSelected((current) => {
      const next = new Set(current)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }

  const allSelected = entries.length > 0 && entries.every((entry) => selected.has(entry.id))

  return (
    <div className="feature-page">
      <div className="feature-heading">
        <div><span className="panel-kicker">{mode === 'users' ? 'Invited applicants' : 'Applicant pipeline'}</span><h2>{mode === 'users' ? 'Beta users' : 'Review the waitlist'}</h2><p>{mode === 'users' ? 'Approved applicants and their invite lifecycle.' : 'Only explicit approval unlocks invite delivery.'}</p></div>
        <span className="count-badge">{entries.length} shown</span>
      </div>
      <div className="filter-bar">
        <label>Search<input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="email" /></label>
        {mode === 'waitlist' && <label>Status<select value={status} onChange={(event) => setStatus(event.target.value as StatusFilter)}><option value="all">All statuses</option><option value="pending">Pending</option><option value="approved">Approved</option><option value="invited">Invited</option><option value="rejected">Rejected</option></select></label>}
        <label>Profile<select value={profile} onChange={(event) => setProfile(event.target.value)}><option value="">All profiles</option><option value="active_trader">Active trader</option><option value="trading_team">Trading team</option><option value="researching">Researching</option></select></label>
        <label>Volume<select value={volume} onChange={(event) => setVolume(event.target.value)}><option value="">All volumes</option><option value="under_10k">Under $10k</option><option value="10k_50k">$10k-$50k</option><option value="50k_100k">$50k-$100k</option><option value="100k_1m">$100k-$1m</option><option value="1m_10m">$1m-$10m</option><option value="10m_plus">$10m+</option></select></label>
      </div>
      {mode === 'waitlist' && selected.size > 0 && <div className="bulk-bar"><strong>{selected.size} selected</strong><button type="button" className="primary-button" onClick={() => applyTransition([...selected], 'approve')}>Approve</button><button type="button" className="danger-button" onClick={() => applyTransition([...selected], 'reject')}>Reject</button></div>}
      {actionError && <p className="inline-error">{actionError}</p>}
      {error && <div className="error-panel"><p>{error}</p><button type="button" onClick={() => setRefresh((value) => value + 1)}>Retry</button></div>}
      {!error && <div className="table-wrap"><table><thead><tr><th><input type="checkbox" aria-label="Select all visible entries" checked={allSelected} onChange={() => setSelected(allSelected ? new Set() : new Set(entries.map((entry) => entry.id)))} /></th><th>Applicant</th><th>Profile</th><th>Volume</th><th>Status</th><th>Submitted</th><th /></tr></thead><tbody>{loading ? <tr><td colSpan={7} className="table-state">Loading waitlist...</td></tr> : entries.length === 0 ? <tr><td colSpan={7} className="table-state">No entries match these filters.</td></tr> : entries.map((entry) => <WaitlistRow key={entry.id} entry={entry} checked={selected.has(entry.id)} onToggle={() => toggle(entry.id)} onDetail={() => getWaitlistDetail(entry.id).then(setDetail).catch((reason: unknown) => setActionError(reason instanceof Error ? reason.message : 'Unable to load applicant.'))} onAction={applyTransition} />)}</tbody></table></div>}
      {detail && <DetailPanel detail={detail} onClose={() => setDetail(null)} onInviteAction={applyInviteAction} />}
    </div>
  )
}

function WaitlistRow({ entry, checked, onToggle, onDetail, onAction }: { entry: WaitlistEntry; checked: boolean; onToggle: () => void; onDetail: () => void; onAction: (ids: string[], transition: 'approve' | 'reject') => Promise<void> }) {
  return <tr><td><input type="checkbox" aria-label={`Select ${entry.email}`} checked={checked} onChange={onToggle} /></td><td><button type="button" className="link-button" onClick={onDetail}>{entry.email}</button><small>{entry.id}</small></td><td>{formatLabel(entry.profile)}</td><td>{formatLabel(entry.monthly_volume)}</td><td><span className={`status status-${entry.status}`}>{entry.status}</span></td><td>{formatDate(entry.created_at)}</td><td className="row-actions">{entry.status === 'pending' && <><button type="button" className="icon-action" onClick={() => onAction([entry.id], 'approve')}>Approve</button><button type="button" className="icon-action reject" onClick={() => onAction([entry.id], 'reject')}>Reject</button></>}</td></tr>
}

function DetailPanel({ detail, onClose, onInviteAction }: { detail: WaitlistDetail; onClose: () => void; onInviteAction: (action: 'issue' | 'resend' | 'revoke', id: string) => Promise<void> }) {
  return <div className="detail-panel"><div className="detail-heading"><div><span className="panel-kicker">Applicant detail</span><h3>{detail.entry.email}</h3></div><button type="button" className="close-button" onClick={onClose}>Close</button></div><div className="detail-grid"><span>Profile<strong>{formatLabel(detail.entry.profile)}</strong></span><span>Volume<strong>{formatLabel(detail.entry.monthly_volume)}</strong></span><span>Status<strong>{detail.entry.status}</strong></span><span>Submitted<strong>{formatDate(detail.entry.created_at)}</strong></span></div><h4>Invite history</h4>{detail.invites.length === 0 ? <div className="invite-empty"><p className="muted">No invite issued.</p>{detail.entry.status === 'approved' && <button type="button" className="primary-button" onClick={() => onInviteAction('issue', detail.entry.id)}>Issue and send invite</button>}</div> : <div className="invite-list">{detail.invites.map((invite) => <div key={invite.id}><span className={`status status-${invite.status}`}>{invite.status}</span><code>{invite.code}</code><span className="muted">{formatDate(invite.created_at)}</span>{invite.status === 'delivery_failed' && <button type="button" className="icon-action" onClick={() => onInviteAction('resend', invite.id)}>Resend</button>}{['issued', 'sent', 'delivery_failed'].includes(invite.status) && <button type="button" className="icon-action reject" onClick={() => onInviteAction('revoke', invite.id)}>Disable invite</button>}</div>)}</div>}<h4>Recent audit</h4>{detail.audit.length === 0 ? <p className="muted">No actions recorded.</p> : <div className="audit-list">{detail.audit.slice(0, 8).map((action) => <div key={action.id}><strong>{action.action}</strong><span>{action.actor_email}</span><span className="muted">{formatDate(action.created_at)}</span></div>)}</div>}</div>
}

function formatLabel(value: string): string { return value.replaceAll('_', ' ') }
function formatDate(value: number): string { return new Date(value * 1_000).toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' }) }
