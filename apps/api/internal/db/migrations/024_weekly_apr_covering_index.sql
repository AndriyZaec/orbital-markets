-- +goose Up
CREATE INDEX idx_snapshots_1h_weekly_apr
    ON market_snapshots_1h(bucket_unix, asset, venue, funding_avg);
DROP INDEX idx_snapshots_1h_bucket;

-- +goose Down
CREATE INDEX idx_snapshots_1h_bucket ON market_snapshots_1h(bucket_unix);
DROP INDEX idx_snapshots_1h_weekly_apr;
