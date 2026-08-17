// Central fetch wrapper for the closed-beta backend.
//
//   - In production, VITE_API_URL is set to https://api.<domain>; this prefix
//     is prepended to the path so the cookie-scoped subdomain is hit directly.
//   - In dev, VITE_API_URL is unset and paths pass through unchanged; the Vite
//     dev server (vite.config.ts) proxies /api → http://localhost:8080.
//   - credentials: 'include' so the __beta cookie (scoped to .<domain>) is sent
//     cross-subdomain to api.<domain>.
//
// Gate detection lives in GateProvider via a /api/v1/health probe — apiFetch
// stays a thin wrapper so genuine 404s from endpoints surface as 404s.

const API_BASE: string = import.meta.env?.VITE_API_URL ?? ''

export function apiUrl(path: string): string {
  return API_BASE + path
}

export function apiFetch(path: string, init?: RequestInit): Promise<Response> {
  return fetch(apiUrl(path), {
    credentials: 'include',
    ...init,
  })
}

function backendError(payload: unknown): string | null {
  if (!payload || typeof payload !== 'object' || !('error' in payload)) return null
  const error = (payload as { error?: unknown }).error
  return typeof error === 'string' && error.trim() ? error : null
}

export function apiError(status: number, fallback: string, payload?: unknown): Error {
  switch (status) {
    case 401:
      return new Error('Your session has expired. Refresh the page and try again.')
    case 408:
    case 504:
      return new Error('The request timed out. Please try again.')
    case 429:
      return new Error('Too many requests. Wait a moment and try again.')
    default:
      if (status >= 500) {
        return new Error('Orbital is temporarily unavailable. Please try again shortly.')
      }
  }

  const detail = backendError(payload)
  if (detail) return new Error(detail)
  if (status === 403) return new Error('This action is not permitted.')
  if (status === 409) return new Error(`${fallback} Refresh the latest data and try again.`)
  return new Error(fallback)
}

export async function apiResponseError(response: Response, fallback: string): Promise<Error> {
  const payload: unknown = await response.json().catch(() => null)
  return apiError(response.status, fallback, payload)
}

export function userErrorMessage(cause: unknown, fallback: string): string {
  if (cause instanceof TypeError && /failed to fetch|load failed|network(?:error| request)/i.test(cause.message)) {
    return 'Unable to reach Orbital. Check your connection and try again.'
  }
  return cause instanceof Error && cause.message ? cause.message : fallback
}
