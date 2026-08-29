export interface Env {
  ASSETS: Fetcher
  BETA_INVITES: KVNamespace
  WAITLIST_DB: D1Database
  RESEND_API_KEY?: string
  RESEND_API_URL?: string
  TEAM_DOMAIN: string
  POLICY_AUD: string
  INVITE_FROM_EMAIL?: string
  APP_ORIGIN: string
  INVITE_SENDING_ENABLED?: string
  ANALYTICS_API_URL?: string
  ANALYTICS_API_TOKEN?: string
}

export interface AccessIdentity {
  email: string
}
