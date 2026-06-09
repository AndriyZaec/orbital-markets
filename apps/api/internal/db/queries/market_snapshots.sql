-- name: InsertSnapshot :exec
INSERT INTO market_snapshots (
    venue, asset, market_key,
    mark_price, index_price, funding_rate,
    bid_price, ask_price, open_interest,
    ts_unix
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetLatestSnapshot :one
SELECT * FROM market_snapshots
WHERE venue = ? AND asset = ?
ORDER BY ts_unix DESC
LIMIT 1;

-- name: ListSnapshotsByAsset :many
SELECT * FROM market_snapshots
WHERE asset = ? AND ts_unix >= ? AND ts_unix <= ?
ORDER BY ts_unix;

-- name: ListSnapshotsByVenueAsset :many
SELECT * FROM market_snapshots
WHERE venue = ? AND asset = ? AND ts_unix >= ? AND ts_unix <= ?
ORDER BY ts_unix;
