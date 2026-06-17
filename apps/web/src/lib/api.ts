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

const API_BASE: string = (import.meta.env.VITE_API_URL ?? '') as string

export function apiFetch(path: string, init?: RequestInit): Promise<Response> {
  return fetch(API_BASE + path, {
    credentials: 'include',
    ...init,
  })
}
