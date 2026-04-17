-- +goose Up
CREATE TABLE paper_positions (
    id TEXT PRIMARY KEY,
    plan_id TEXT NOT NULL,
    opportunity_id TEXT NOT NULL,
    asset TEXT NOT NULL,
    direction TEXT NOT NULL,
    venue_a TEXT NOT NULL,
    venue_b TEXT NOT NULL,
    state TEXT NOT NULL,
    target_notional REAL NOT NULL,
    entry_spread REAL NOT NULL DEFAULT 0,
    hedge_mismatch REAL NOT NULL DEFAULT 0,
    close_reason TEXT NOT NULL DEFAULT '',
    price_pnl REAL NOT NULL DEFAULT 0,
    funding_pnl REAL NOT NULL DEFAULT 0,
    total_pnl REAL NOT NULL DEFAULT 0,
    realized_pnl REAL NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    opened_at TEXT,
    closed_at TEXT,
    updated_at TEXT NOT NULL
);

CREATE TABLE paper_fills (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    position_id TEXT NOT NULL REFERENCES paper_positions(id),
    leg INTEGER NOT NULL, -- 1 or 2
    venue TEXT NOT NULL,
    side TEXT NOT NULL,
    target_size REAL NOT NULL,
    filled_size REAL NOT NULL,
    fill_price REAL NOT NULL,
    slippage REAL NOT NULL,
    fee REAL NOT NULL,
    filled_at TEXT NOT NULL
);

CREATE TABLE paper_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    position_id TEXT NOT NULL REFERENCES paper_positions(id),
    from_state TEXT NOT NULL,
    to_state TEXT NOT NULL,
    reason TEXT NOT NULL,
    at TEXT NOT NULL
);

CREATE INDEX idx_paper_positions_state ON paper_positions(state);
CREATE INDEX idx_paper_positions_asset ON paper_positions(asset);
CREATE INDEX idx_paper_events_position ON paper_events(position_id);
CREATE INDEX idx_paper_fills_position ON paper_fills(position_id);

-- +goose Down
DROP TABLE paper_events;
DROP TABLE paper_fills;
DROP TABLE paper_positions;
