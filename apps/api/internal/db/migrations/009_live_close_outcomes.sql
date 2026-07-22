-- +goose Up
CREATE TABLE live_close_outcomes (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    position_id TEXT NOT NULL REFERENCES live_positions(id),
    leg INTEGER NOT NULL,
    venue TEXT NOT NULL,
    client_order_id TEXT NOT NULL,
    order_id TEXT NOT NULL DEFAULT '',
    requested_amount REAL NOT NULL,
    filled_amount REAL NOT NULL DEFAULT 0,
    avg_fill_price REAL NOT NULL DEFAULT 0,
    fill_ratio REAL NOT NULL DEFAULT 0,
    accepted INTEGER NOT NULL DEFAULT 0,
    confirmed INTEGER NOT NULL DEFAULT 0,
    resolved INTEGER NOT NULL DEFAULT 0,
    error TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE(position_id, client_order_id)
);

CREATE INDEX idx_live_close_outcomes_position_leg
    ON live_close_outcomes(position_id, leg, id);

-- +goose Down
DROP TABLE live_close_outcomes;
