-- name: InsertSnapshot :exec
INSERT INTO market_snapshots (
    venue, asset, market_key,
    mark_price, index_price, funding_rate,
    bid_price, ask_price, open_interest,
    timestamp
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetLatestSnapshot :one
SELECT * FROM market_snapshots
WHERE venue = ? AND asset = ?
ORDER BY timestamp DESC
LIMIT 1;

-- name: ListSnapshotsByAsset :many
SELECT * FROM market_snapshots
WHERE asset = ? AND timestamp >= ? AND timestamp <= ?
ORDER BY timestamp;

-- name: ListSnapshotsByVenueAsset :many
SELECT * FROM market_snapshots
WHERE venue = ? AND asset = ? AND timestamp >= ? AND timestamp <= ?
ORDER BY timestamp;
