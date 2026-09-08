-- +goose Up
ALTER TABLE live_sessions ADD COLUMN account_bindings_json TEXT NOT NULL DEFAULT '';
ALTER TABLE live_sessions ADD COLUMN account_bindings_key TEXT NOT NULL DEFAULT '';

UPDATE live_sessions
SET account_bindings_json = json_object(
        'hyperliquid', lower(trim(account_hyperliquid)),
        'pacifica', trim(account_pacifica)
    ),
    account_bindings_key = json_object(
        'hyperliquid', lower(trim(account_hyperliquid)),
        'pacifica', trim(account_pacifica)
    )
WHERE trim(account_pacifica) != '' AND trim(account_hyperliquid) != '';

DROP INDEX idx_live_sessions_active_account_asset;

CREATE UNIQUE INDEX idx_live_sessions_active_bindings_asset
    ON live_sessions(
        CASE
            WHEN account_bindings_key != '' THEN account_bindings_key
            ELSE json_object(
                'hyperliquid', lower(trim(account_hyperliquid)),
                'pacifica', trim(account_pacifica)
            )
        END,
        asset
    )
    WHERE terminal_at IS NULL;

ALTER TABLE live_positions ADD COLUMN account_bindings_json TEXT NOT NULL DEFAULT '';
ALTER TABLE live_positions ADD COLUMN account_bindings_key TEXT NOT NULL DEFAULT '';

UPDATE live_positions
SET account_bindings_json = json_object(
        'hyperliquid', lower(trim(account_hyperliquid)),
        'pacifica', trim(account_pacifica)
    ),
    account_bindings_key = json_object(
        'hyperliquid', lower(trim(account_hyperliquid)),
        'pacifica', trim(account_pacifica)
    )
WHERE trim(account_pacifica) != '' AND trim(account_hyperliquid) != '';

CREATE INDEX idx_live_positions_account_bindings
    ON live_positions(account_bindings_key, started_at);

-- +goose Down
DROP INDEX idx_live_positions_account_bindings;
ALTER TABLE live_positions DROP COLUMN account_bindings_key;
ALTER TABLE live_positions DROP COLUMN account_bindings_json;

DROP INDEX idx_live_sessions_active_bindings_asset;
CREATE UNIQUE INDEX idx_live_sessions_active_account_asset
    ON live_sessions(account_pacifica, account_hyperliquid, asset)
    WHERE terminal_at IS NULL;
ALTER TABLE live_sessions DROP COLUMN account_bindings_key;
ALTER TABLE live_sessions DROP COLUMN account_bindings_json;
