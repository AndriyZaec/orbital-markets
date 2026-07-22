-- +goose Up
CREATE TABLE live_sessions (
    id TEXT PRIMARY KEY,
    state TEXT NOT NULL,
    payload TEXT NOT NULL,
    account_pacifica TEXT NOT NULL,
    account_hyperliquid TEXT NOT NULL,
    asset TEXT NOT NULL,
    has_exposure INTEGER NOT NULL DEFAULT 0,
    expires_at TEXT NOT NULL,
    recovery_detail TEXT NOT NULL DEFAULT '',
    recovery_owner TEXT NOT NULL DEFAULT '',
    recovery_lease_until TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    terminal_at TEXT
);

CREATE INDEX idx_live_sessions_active
    ON live_sessions(terminal_at, has_exposure, expires_at);

CREATE UNIQUE INDEX idx_live_sessions_active_account_asset
    ON live_sessions(account_pacifica, account_hyperliquid, asset)
    WHERE terminal_at IS NULL;

-- +goose Down
DROP TABLE live_sessions;
