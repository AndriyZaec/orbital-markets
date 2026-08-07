declare namespace Cloudflare {
  interface GlobalProps {
    mainModule: typeof import('../src/index');
  }

  interface Env {
    WAITLIST_DB: D1Database;
    TEST_MIGRATIONS: import('cloudflare:test').D1Migration[];
  }
}
