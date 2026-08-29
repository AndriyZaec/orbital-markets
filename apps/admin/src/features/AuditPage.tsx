import { useEffect, useState } from 'react'
import { listAudit, type AuditRecord } from '../api'

export function AuditPage() {
  const [records, setRecords] = useState<AuditRecord[]>([])
  const [error, setError] = useState<string | null>(null)
  useEffect(() => {
    const controller = new AbortController()
    listAudit(controller.signal).then((response) => setRecords(response.items)).catch((reason: unknown) => { if (!controller.signal.aborted) setError(reason instanceof Error ? reason.message : 'Unable to load audit log.') })
    return () => controller.abort()
  }, [])
  return <div className="feature-page"><div className="feature-heading"><div><span className="panel-kicker">Immutable record</span><h2>Audit log</h2><p>Every mutation is tied to a verified Cloudflare Access identity.</p></div></div>{error && <p className="inline-error">{error}</p>}<div className="table-wrap"><table><thead><tr><th>Action</th><th>Actor</th><th>Target</th><th>Time</th></tr></thead><tbody>{records.length === 0 && !error ? <tr><td colSpan={4} className="table-state">No admin actions recorded.</td></tr> : records.map((record) => <tr key={record.id}><td><span className="status status-approved">{record.action}</span></td><td>{record.actor_email}</td><td><code>{record.target_type}/{record.target_id}</code></td><td>{new Date(record.created_at * 1_000).toLocaleString()}</td></tr>)}</tbody></table></div></div>
}
