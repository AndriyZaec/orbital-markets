-- +goose Up
CREATE TABLE live_positions (
    id TEXT PRIMARY KEY,
    plan_id TEXT NOT NULL,
    opportunity_id TEXT NOT NULL,
    asset TEXT NOT NULL,
    venue_a TEXT NOT NULL,
    venue_b TEXT NOT NULL,
    state TEXT NOT NULL,             -- open, degraded, failed
    notional REAL NOT NULL,
    leverage REAL NOT NULL,
    entry_spread REAL NOT NULL DEFAULT 0,
    hedge_mismatch REAL NOT NULL DEFAULT 0,
    started_at TEXT NOT NULL,
    opened_at TEXT,
    completed_at TEXT,
    updated_at TEXT NOT NULL
);

CREATE TABLE live_fills (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    position_id TEXT NOT NULL REFERENCES live_positions(id),
    leg INTEGER NOT NULL,            -- 1 or 2
    venue TEXT NOT NULL,
    symbol TEXT NOT NULL,
    side TEXT NOT NULL,
    order_id TEXT NOT NULL DEFAULT '',
    client_order_id TEXT NOT NULL DEFAULT '',
    requested_amount REAL NOT NULL,
    filled_amount REAL NOT NULL,
    avg_fill_price REAL NOT NULL,
    fill_ratio REAL NOT NULL,
    fee REAL NOT NULL,
    accepted INTEGER NOT NULL DEFAULT 0,
    filled INTEGER NOT NULL DEFAULT 0,
    error TEXT NOT NULL DEFAULT '',
    filled_at TEXT NOT NULL
);

CREATE TABLE live_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    position_id TEXT NOT NULL REFERENCES live_positions(id),
    event TEXT NOT NULL,              -- leg1_submit, leg1_fill, leg2_submit, leg2_fill, retry, unwind, complete
    state TEXT NOT NULL,              -- executor state at this point
    detail TEXT NOT NULL DEFAULT '',
    at TEXT NOT NULL
);

CREATE INDEX idx_live_positions_state ON live_positions(state);
CREATE INDEX idx_live_positions_asset ON live_positions(asset);
CREATE INDEX idx_live_fills_position ON live_fills(position_id);
CREATE INDEX idx_live_events_position ON live_events(position_id);

-- +goose Down
DROP TABLE live_events;
DROP TABLE live_fills;
DROP TABLE live_positions;
