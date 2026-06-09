-- +goose Up
ALTER TABLE market_snapshots ADD COLUMN ts_unix INTEGER NOT NULL DEFAULT 0;
UPDATE market_snapshots SET ts_unix = CAST(strftime('%s', timestamp) AS INTEGER);
DROP INDEX IF EXISTS idx_snapshots_venue_asset_ts;
DROP INDEX IF EXISTS idx_snapshots_asset_ts;
ALTER TABLE market_snapshots DROP COLUMN timestamp;
CREATE INDEX idx_snapshots_venue_asset_ts ON market_snapshots(venue, asset, ts_unix);
CREATE INDEX idx_snapshots_asset_ts ON market_snapshots(asset, ts_unix);

-- +goose Down
ALTER TABLE market_snapshots ADD COLUMN timestamp TEXT NOT NULL DEFAULT '';
UPDATE market_snapshots SET timestamp = datetime(ts_unix, 'unixepoch');
DROP INDEX IF EXISTS idx_snapshots_venue_asset_ts;
DROP INDEX IF EXISTS idx_snapshots_asset_ts;
ALTER TABLE market_snapshots DROP COLUMN ts_unix;
CREATE INDEX idx_snapshots_venue_asset_ts ON market_snapshots(venue, asset, timestamp);
CREATE INDEX idx_snapshots_asset_ts ON market_snapshots(asset, timestamp);
