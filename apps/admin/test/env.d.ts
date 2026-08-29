declare namespace Cloudflare {
  interface GlobalProps {
    mainModule: typeof import('../worker/index')
  }

  interface Env {
    BETA_INVITES: KVNamespace
    WAITLIST_DB: D1Database
    TEST_MIGRATIONS: import('cloudflare:test').D1Migration[]
  }
}
