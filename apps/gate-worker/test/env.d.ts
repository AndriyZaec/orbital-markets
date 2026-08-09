declare namespace Cloudflare {
  interface GlobalProps {
    mainModule: typeof import('../src/index');
  }

  interface Env {
    BETA_INVITES: KVNamespace;
    WAITLIST_DB: D1Database;
    JWT_SECRET: string;
    COOKIE_DOMAIN: string;
    TEST_MIGRATIONS: import('cloudflare:test').D1Migration[];
  }
}
