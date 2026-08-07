CREATE TABLE waitlist_entries (
  id TEXT PRIMARY KEY,
  email TEXT NOT NULL UNIQUE,
  profile TEXT NOT NULL CHECK (profile IN ('active_trader', 'trading_team', 'researching')),
  monthly_volume TEXT NOT NULL CHECK (
    monthly_volume IN ('under_10k', '10k_50k', '50k_100k', '100k_1m', '1m_10m', '10m_plus')
  ),
  source TEXT NOT NULL CHECK (source = 'landing'),
  status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'rejected', 'invited')),
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
) STRICT;

CREATE INDEX waitlist_entries_status_created_at_idx
  ON waitlist_entries (status, created_at);
