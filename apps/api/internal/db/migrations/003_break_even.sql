-- +goose Up
ALTER TABLE paper_positions ADD COLUMN risk_tier TEXT NOT NULL DEFAULT '';
ALTER TABLE paper_positions ADD COLUMN est_break_even_hours REAL NOT NULL DEFAULT 0;
ALTER TABLE paper_positions ADD COLUMN break_even_reached INTEGER NOT NULL DEFAULT 0;
ALTER TABLE paper_positions ADD COLUMN hold_hours REAL NOT NULL DEFAULT 0;

-- +goose Down
-- SQLite doesn't support DROP COLUMN before 3.35.0, recreate table would be needed.
-- For dev purposes, these columns are safe to leave.
