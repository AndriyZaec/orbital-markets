-- +goose Up
ALTER TABLE live_positions ADD COLUMN account_pacifica TEXT NOT NULL DEFAULT '';
ALTER TABLE live_positions ADD COLUMN account_hyperliquid TEXT NOT NULL DEFAULT '';

UPDATE live_positions
SET account_pacifica = COALESCE((
        SELECT account_pacifica
        FROM live_sessions
        WHERE live_sessions.id = live_positions.plan_id
           OR CASE WHEN json_valid(live_sessions.payload)
                THEN json_extract(live_sessions.payload, '$.plan.id')
              END = live_positions.plan_id
        ORDER BY live_sessions.updated_at DESC
        LIMIT 1
    ), ''),
    account_hyperliquid = COALESCE((
        SELECT account_hyperliquid
        FROM live_sessions
        WHERE live_sessions.id = live_positions.plan_id
           OR CASE WHEN json_valid(live_sessions.payload)
                THEN json_extract(live_sessions.payload, '$.plan.id')
              END = live_positions.plan_id
        ORDER BY live_sessions.updated_at DESC
        LIMIT 1
    ), '');

CREATE INDEX idx_live_positions_accounts
    ON live_positions(account_pacifica, account_hyperliquid, started_at);

-- +goose Down
DROP INDEX idx_live_positions_accounts;
ALTER TABLE live_positions DROP COLUMN account_hyperliquid;
ALTER TABLE live_positions DROP COLUMN account_pacifica;
