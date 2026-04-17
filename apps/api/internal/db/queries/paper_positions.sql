-- name: InsertPosition :exec
INSERT INTO paper_positions (
    id, plan_id, opportunity_id, asset, direction,
    venue_a, venue_b, state, target_notional,
    entry_spread, hedge_mismatch, close_reason,
    price_pnl, funding_pnl, total_pnl, realized_pnl,
    created_at, opened_at, closed_at, updated_at
) VALUES (
    ?, ?, ?, ?, ?,
    ?, ?, ?, ?,
    ?, ?, ?,
    ?, ?, ?, ?,
    ?, ?, ?, ?
);

-- name: UpdatePosition :exec
UPDATE paper_positions SET
    state = ?,
    entry_spread = ?,
    hedge_mismatch = ?,
    close_reason = ?,
    price_pnl = ?,
    funding_pnl = ?,
    total_pnl = ?,
    realized_pnl = ?,
    opened_at = ?,
    closed_at = ?,
    updated_at = ?
WHERE id = ?;

-- name: GetPosition :one
SELECT * FROM paper_positions WHERE id = ?;

-- name: ListPositions :many
SELECT * FROM paper_positions ORDER BY created_at DESC;

-- name: ListOpenPositions :many
SELECT * FROM paper_positions WHERE state IN ('open', 'degraded') ORDER BY created_at DESC;

-- name: InsertFill :exec
INSERT INTO paper_fills (
    position_id, leg, venue, side,
    target_size, filled_size, fill_price,
    slippage, fee, filled_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetFillsByPosition :many
SELECT * FROM paper_fills WHERE position_id = ? ORDER BY leg;

-- name: InsertEvent :exec
INSERT INTO paper_events (
    position_id, from_state, to_state, reason, at
) VALUES (?, ?, ?, ?, ?);

-- name: GetEventsByPosition :many
SELECT * FROM paper_events WHERE position_id = ? ORDER BY id;
