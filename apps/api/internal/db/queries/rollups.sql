-- name: UpsertSnapshot5m :exec
INSERT INTO market_snapshots_5m (
    venue, asset, bucket_unix,
    open, high, low, close,
    funding_avg, oi_avg, bid_avg, ask_avg
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(venue, asset, bucket_unix) DO UPDATE SET
    open        = excluded.open,
    high        = excluded.high,
    low         = excluded.low,
    close       = excluded.close,
    funding_avg = excluded.funding_avg,
    oi_avg      = excluded.oi_avg,
    bid_avg     = excluded.bid_avg,
    ask_avg     = excluded.ask_avg;

-- name: UpsertSnapshot1h :exec
INSERT INTO market_snapshots_1h (
    venue, asset, bucket_unix,
    open, high, low, close,
    funding_avg, oi_avg, bid_avg, ask_avg
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(venue, asset, bucket_unix) DO UPDATE SET
    open        = excluded.open,
    high        = excluded.high,
    low         = excluded.low,
    close       = excluded.close,
    funding_avg = excluded.funding_avg,
    oi_avg      = excluded.oi_avg,
    bid_avg     = excluded.bid_avg,
    ask_avg     = excluded.ask_avg;

-- name: List5mByVenueAsset :many
SELECT * FROM market_snapshots_5m
WHERE venue = ? AND asset = ? AND bucket_unix >= ? AND bucket_unix <= ?
ORDER BY bucket_unix;

-- name: List1hByVenueAsset :many
SELECT * FROM market_snapshots_1h
WHERE venue = ? AND asset = ? AND bucket_unix >= ? AND bucket_unix <= ?
ORDER BY bucket_unix;
