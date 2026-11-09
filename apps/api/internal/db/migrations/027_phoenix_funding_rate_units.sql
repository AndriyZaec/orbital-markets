-- +goose Up
-- Before this migration, the Phoenix adapter stored percentage points as
-- fractions. Repair recorded rates and rollups once, before recording new data.
-- Cash funding payments and position accounting are already USD and unaffected.
UPDATE market_snapshots SET funding_rate = funding_rate / 100.0 WHERE venue = 'phoenix';
UPDATE market_snapshots_5m SET funding_avg = funding_avg / 100.0 WHERE venue = 'phoenix';
UPDATE market_snapshots_1h SET funding_avg = funding_avg / 100.0 WHERE venue = 'phoenix';

-- +goose Down
-- Do not restore incorrect units or multiply newly recorded fractional rates.
SELECT 1;
