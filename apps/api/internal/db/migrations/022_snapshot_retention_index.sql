-- +goose Up
CREATE INDEX idx_snapshots_ts ON market_snapshots(ts_unix);

-- +goose Down
DROP INDEX idx_snapshots_ts;
