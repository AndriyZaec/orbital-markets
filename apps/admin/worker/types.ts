export interface Env {
  ASSETS: Fetcher
  BETA_INVITES: KVNamespace
  WAITLIST_DB: D1Database
  EMAIL?: SendEmail
  TEAM_DOMAIN: string
  POLICY_AUD: string
  INVITE_FROM_EMAIL?: string
  APP_ORIGIN?: string
}

export interface AccessIdentity {
  email: string
}
