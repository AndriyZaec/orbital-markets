import { useState } from 'react'
import { apiError, apiFetch, userErrorMessage } from '@/lib/api'
import { validatedTelegramURL } from '@/lib/telegram'

interface TelegramLinkResponse {
  url?: string
  error?: string
}

export function useTelegramLink() {
  const [creating, setCreating] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const connect = async (accountPacifica: string, accountHyperliquid: string) => {
    setCreating(true)
    setError(null)
    const telegramWindow = window.open('about:blank', '_blank')
    try {
      const response = await apiFetch('/api/v1/telegram/link-intents', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          account_pacifica: accountPacifica,
          account_hyperliquid: accountHyperliquid,
        }),
      })
      const body = await response.json().catch(() => ({})) as TelegramLinkResponse
      if (!response.ok) {
        throw apiError(response.status, 'Telegram connection is not available yet. Please try again later.', body)
      }
      const telegramURL = validatedTelegramURL(body.url)
      if (telegramWindow) {
        telegramWindow.opener = null
        telegramWindow.location.replace(telegramURL)
      } else {
        window.location.assign(telegramURL)
      }
    } catch (cause) {
      telegramWindow?.close()
      setError(userErrorMessage(cause, 'Unable to open Telegram. Please try again.'))
    } finally {
      setCreating(false)
    }
  }

  return { connect, creating, error }
}
