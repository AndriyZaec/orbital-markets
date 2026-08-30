PRAGMA defer_foreign_keys = ON;

ALTER TABLE beta_invites RENAME TO beta_invites_old;
ALTER TABLE waitlist_entries RENAME TO waitlist_entries_old;

CREATE TABLE waitlist_entries (
  id TEXT PRIMARY KEY,
  email TEXT NOT NULL UNIQUE,
  profile TEXT NOT NULL CHECK (
    profile IN (
      'active_trader',
      'occasional_trader',
      'defi_user',
      'return_seeker',
      'trading_team',
      'researching'
    )
  ),
  monthly_volume TEXT NOT NULL CHECK (
    monthly_volume IN (
      'under_1k',
      '1k_10k',
      'under_10k',
      '10k_50k',
      '50k_100k',
      '100k_1m',
      '1m_plus',
      '1m_10m',
      '10m_plus'
    )
  ),
  source TEXT NOT NULL CHECK (source = 'landing'),
  status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'rejected', 'invited')),
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
) STRICT;

INSERT INTO waitlist_entries
  (id, email, profile, monthly_volume, source, status, created_at, updated_at)
SELECT id, email, profile, monthly_volume, source, status, created_at, updated_at
FROM waitlist_entries_old;

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

INSERT INTO beta_invites
  (id, waitlist_entry_id, code, status, bound_cookie_id, created_at, sent_at, redeemed_at, revoked_at, delivery_attempts, delivery_error, updated_at)
SELECT id, waitlist_entry_id, code, status, bound_cookie_id, created_at, sent_at, redeemed_at, revoked_at, delivery_attempts, delivery_error, updated_at
FROM beta_invites_old;

DROP TABLE beta_invites_old;
DROP TABLE waitlist_entries_old;

CREATE INDEX waitlist_entries_status_created_at_idx
  ON waitlist_entries (status, created_at);

CREATE UNIQUE INDEX beta_invites_one_active_per_entry_idx
  ON beta_invites (waitlist_entry_id)
  WHERE status IN ('issued', 'sent', 'delivery_failed');

CREATE INDEX beta_invites_entry_created_at_idx
  ON beta_invites (waitlist_entry_id, created_at DESC);
