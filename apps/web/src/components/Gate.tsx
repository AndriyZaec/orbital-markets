import { useState, type FormEvent } from 'react'

// Minimal stealth gate page. No app branding, no marketing copy — just a code
// input. On success the Worker sets the __beta cookie and we reload to let
// GateProvider re-probe /health and render the app.

export function Gate() {
  const [code, setCode] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    if (!code.trim() || submitting) return
    setSubmitting(true)
    setError(null)
    try {
      const resp = await fetch('/gate/redeem', {
        method: 'POST',
        credentials: 'include',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ code: code.trim() }),
      })
      if (!resp.ok) {
        setError('Invalid code.')
        setSubmitting(false)
        return
      }
      window.location.reload()
    } catch {
      setError('Network error.')
      setSubmitting(false)
    }
  }

  return (
    <div className="min-h-screen flex items-center justify-center bg-black text-neutral-200">
      <form onSubmit={onSubmit} className="w-full max-w-xs px-6">
        <input
          type="text"
          value={code}
          onChange={(e) => setCode(e.target.value)}
          placeholder="code"
          autoFocus
          autoComplete="off"
          spellCheck={false}
          className="w-full bg-transparent border-b border-neutral-700 py-2 text-center tracking-widest focus:outline-none focus:border-neutral-400"
        />
        <button
          type="submit"
          disabled={!code.trim() || submitting}
          className="mt-6 w-full text-xs uppercase tracking-widest text-neutral-500 hover:text-neutral-200 disabled:opacity-40"
        >
          {submitting ? '…' : 'enter'}
        </button>
        {error && <p className="mt-4 text-center text-xs text-red-400">{error}</p>}
      </form>
    </div>
  )
}
