CREATE TABLE beta_invites (
  id TEXT PRIMARY KEY,
  waitlist_entry_id TEXT NOT NULL REFERENCES waitlist_entries(id),
  code TEXT NOT NULL UNIQUE,
  status TEXT NOT NULL CHECK (status IN ('issued', 'sent', 'delivery_failed', 'redeemed', 'revoked')),
  bound_cookie_id TEXT,
  created_at INTEGER NOT NULL,
  sent_at INTEGER,
  redeemed_at INTEGER,
  revoked_at INTEGER,
  delivery_attempts INTEGER NOT NULL DEFAULT 0,
  delivery_error TEXT,
  updated_at INTEGER NOT NULL
) STRICT;

CREATE UNIQUE INDEX beta_invites_one_active_per_entry_idx
  ON beta_invites (waitlist_entry_id)
  WHERE status IN ('issued', 'sent', 'delivery_failed');

CREATE INDEX beta_invites_entry_created_at_idx
  ON beta_invites (waitlist_entry_id, created_at DESC);

CREATE TABLE admin_actions (
  id TEXT PRIMARY KEY,
  actor_email TEXT NOT NULL,
  action TEXT NOT NULL,
  target_type TEXT NOT NULL,
  target_id TEXT NOT NULL,
  idempotency_key TEXT,
  metadata_json TEXT NOT NULL DEFAULT '{}',
  created_at INTEGER NOT NULL
) STRICT;

CREATE UNIQUE INDEX admin_actions_actor_idempotency_idx
  ON admin_actions (actor_email, idempotency_key)
  WHERE idempotency_key IS NOT NULL;

CREATE INDEX admin_actions_target_created_at_idx
  ON admin_actions (target_type, target_id, created_at DESC);

CREATE INDEX admin_actions_created_at_idx
  ON admin_actions (created_at DESC);
