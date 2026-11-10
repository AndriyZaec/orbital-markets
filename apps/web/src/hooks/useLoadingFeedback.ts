import { useEffect, useState } from 'react'

// Presentation only: these delays never extend data freshness or authorize work.
export const LOADING_INDICATOR_DELAY_MS = 300
export const LOADING_UNAVAILABLE_AFTER_MS = 20_000

export function useLoadingFeedback(pending: boolean, identity: string) {
  const key = JSON.stringify([identity, pending])
  const [feedback, setFeedback] = useState({ key, phase: 'quiet' as 'quiet' | 'loading' | 'unavailable' })
  if (feedback.key !== key) setFeedback({ key, phase: 'quiet' })

  useEffect(() => {
    if (!pending) return
    const indicator = setTimeout(() => setFeedback({ key, phase: 'loading' }), LOADING_INDICATOR_DELAY_MS)
    const unavailable = setTimeout(() => setFeedback({ key, phase: 'unavailable' }), LOADING_UNAVAILABLE_AFTER_MS)
    return () => { clearTimeout(indicator); clearTimeout(unavailable) }
  }, [key, pending])

  return pending && feedback.key === key ? feedback.phase : 'quiet'
}
