import { useEffect, useState } from 'react'

function isPageVisible(): boolean {
  return typeof document === 'undefined' || document.visibilityState !== 'hidden'
}

export function usePageVisibility(): boolean {
  const [visible, setVisible] = useState(isPageVisible)

  useEffect(() => {
    const update = () => setVisible(isPageVisible())
    document.addEventListener('visibilitychange', update)
    return () => document.removeEventListener('visibilitychange', update)
  }, [])

  return visible
}
