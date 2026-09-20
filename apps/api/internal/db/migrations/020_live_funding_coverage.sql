-- +goose Up
ALTER TABLE live_funding_payments ADD COLUMN paid_at_ms INTEGER;

UPDATE live_funding_payments
SET paid_at_ms = CAST(ROUND((julianday(paid_at) - 2440587.5) * 86400000) AS INTEGER);

DELETE FROM live_funding_payments
WHERE position_id IN (
    SELECT position_id FROM live_funding_sync WHERE finalized = 1
);

CREATE INDEX idx_live_funding_payments_compaction
    ON live_funding_payments(position_id, venue, paid_at_ms);

CREATE TABLE live_funding_coverage (
    position_id TEXT NOT NULL REFERENCES live_positions(id),
    venue TEXT NOT NULL,
    account TEXT NOT NULL,
    coverage_start_ms INTEGER NOT NULL,
    covered_through_ms INTEGER,
    amount_usd REAL NOT NULL DEFAULT 0,
    payment_count INTEGER NOT NULL DEFAULT 0,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (position_id, venue),
    CHECK (covered_through_ms IS NULL OR covered_through_ms >= coverage_start_ms)
);

UPDATE live_positions
SET funding_pnl_source = 'estimated'
WHERE state IN ('open', 'closing')
  AND (venue_a = 'aster' OR venue_b = 'aster');

-- +goose Down
DROP TABLE live_funding_coverage;
DROP INDEX idx_live_funding_payments_compaction;
ALTER TABLE live_funding_payments DROP COLUMN paid_at_ms;
