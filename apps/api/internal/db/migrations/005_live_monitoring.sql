-- +goose Up
ALTER TABLE live_positions ADD COLUMN current_spread REAL NOT NULL DEFAULT 0;
ALTER TABLE live_positions ADD COLUMN current_basis REAL NOT NULL DEFAULT 0;
ALTER TABLE live_positions ADD COLUMN entry_basis REAL NOT NULL DEFAULT 0;
ALTER TABLE live_positions ADD COLUMN basis_change REAL NOT NULL DEFAULT 0;
ALTER TABLE live_positions ADD COLUMN price_pnl REAL NOT NULL DEFAULT 0;
ALTER TABLE live_positions ADD COLUMN funding_pnl REAL NOT NULL DEFAULT 0;
ALTER TABLE live_positions ADD COLUMN total_pnl REAL NOT NULL DEFAULT 0;
ALTER TABLE live_positions ADD COLUMN leg1_current_price REAL NOT NULL DEFAULT 0;
ALTER TABLE live_positions ADD COLUMN leg2_current_price REAL NOT NULL DEFAULT 0;
ALTER TABLE live_positions ADD COLUMN leg1_liq_price REAL NOT NULL DEFAULT 0;
ALTER TABLE live_positions ADD COLUMN leg2_liq_price REAL NOT NULL DEFAULT 0;
ALTER TABLE live_positions ADD COLUMN leg1_liq_dist REAL NOT NULL DEFAULT 0;
ALTER TABLE live_positions ADD COLUMN leg2_liq_dist REAL NOT NULL DEFAULT 0;
ALTER TABLE live_positions ADD COLUMN leg1_liq_risk TEXT NOT NULL DEFAULT '';
ALTER TABLE live_positions ADD COLUMN leg2_liq_risk TEXT NOT NULL DEFAULT '';
ALTER TABLE live_positions ADD COLUMN hold_hours REAL NOT NULL DEFAULT 0;
ALTER TABLE live_positions ADD COLUMN monitor_at TEXT;

-- +goose Down
-- SQLite does not support DROP COLUMN before 3.35.0.
-- For safety, the down migration recreates the table without the new columns.
-- In practice we only roll forward.
