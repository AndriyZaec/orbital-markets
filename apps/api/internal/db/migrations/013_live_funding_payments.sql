-- +goose Up
ALTER TABLE live_positions ADD COLUMN funding_pnl_source TEXT NOT NULL DEFAULT 'pending';

UPDATE live_positions
SET funding_pnl_source = 'estimated'
WHERE state = 'closed' OR funding_pnl != 0;

CREATE TABLE live_funding_payments (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    position_id TEXT NOT NULL REFERENCES live_positions(id),
    venue TEXT NOT NULL,
    account TEXT NOT NULL,
    external_id TEXT NOT NULL,
    asset TEXT NOT NULL,
    amount_usd REAL NOT NULL,
    paid_at TEXT NOT NULL,
    created_at TEXT NOT NULL,
    UNIQUE(venue, account, external_id)
);

CREATE INDEX idx_live_funding_payments_position
    ON live_funding_payments(position_id, paid_at);

CREATE TABLE live_funding_sync (
    position_id TEXT PRIMARY KEY REFERENCES live_positions(id),
    synced_at TEXT NOT NULL,
    finalized INTEGER NOT NULL DEFAULT 0
);

INSERT INTO live_funding_sync (position_id, synced_at, finalized)
SELECT id, updated_at, 1 FROM live_positions WHERE state = 'closed';

-- +goose Down
DROP TABLE live_funding_sync;
DROP TABLE live_funding_payments;
ALTER TABLE live_positions DROP COLUMN funding_pnl_source;
