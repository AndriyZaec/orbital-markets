import { applyD1Migrations } from 'cloudflare:test';
import { env } from 'cloudflare:workers';

const [waitlistMigration, adminMigration, ...upgradeMigrations] = env.TEST_MIGRATIONS;
if (!waitlistMigration || !adminMigration || upgradeMigrations.length === 0) {
  throw new Error('Expected baseline and upgrade D1 migrations');
}

await applyD1Migrations(env.WAITLIST_DB, [waitlistMigration, adminMigration]);
await env.WAITLIST_DB.batch([
  env.WAITLIST_DB.prepare(
    `INSERT INTO waitlist_entries
      (id, email, profile, monthly_volume, source, status, created_at, updated_at)
     VALUES ('migration-entry', 'migration@example.com', 'active_trader', '100k_1m', 'landing', 'approved', 1, 1)`,
  ),
  env.WAITLIST_DB.prepare(
    `INSERT INTO beta_invites
      (id, waitlist_entry_id, code, status, created_at, updated_at)
     VALUES ('migration-invite', 'migration-entry', 'migration-code', 'sent', 1, 1)`,
  ),
]);

await applyD1Migrations(env.WAITLIST_DB, upgradeMigrations);
const entry = await env.WAITLIST_DB.prepare(
  "SELECT id FROM waitlist_entries WHERE id = 'migration-entry'",
).first();
const invite = await env.WAITLIST_DB.prepare(
  "SELECT id FROM beta_invites WHERE id = 'migration-invite'",
).first();
const foreignKeyFailures = await env.WAITLIST_DB.prepare('PRAGMA foreign_key_check').all();
if (!entry || !invite || foreignKeyFailures.results.length > 0) {
  throw new Error('Waitlist option migration did not preserve linked invite data');
}

await env.WAITLIST_DB.batch([
  env.WAITLIST_DB.prepare("DELETE FROM beta_invites WHERE id = 'migration-invite'"),
  env.WAITLIST_DB.prepare("DELETE FROM waitlist_entries WHERE id = 'migration-entry'"),
]);
