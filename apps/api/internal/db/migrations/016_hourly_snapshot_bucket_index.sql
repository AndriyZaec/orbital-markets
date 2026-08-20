-- +goose Up
CREATE INDEX idx_snapshots_1h_bucket ON market_snapshots_1h(bucket_unix);

-- +goose Down
DROP INDEX idx_snapshots_1h_bucket;
