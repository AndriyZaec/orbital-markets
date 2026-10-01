-- +goose Up
CREATE INDEX idx_live_positions_bindings_opened
    ON live_positions(account_bindings_key, opened_at DESC, id DESC)
    WHERE state != 'closed' AND opened_at IS NOT NULL;

CREATE INDEX idx_live_positions_bindings_completed
    ON live_positions(account_bindings_key, completed_at DESC, id DESC)
    WHERE state = 'closed' AND completed_at IS NOT NULL;

CREATE INDEX idx_live_positions_legacy_opened
    ON live_positions(account_pacifica, account_hyperliquid, opened_at DESC, id DESC)
    WHERE account_bindings_key = '' AND state != 'closed' AND opened_at IS NOT NULL;

CREATE INDEX idx_live_positions_legacy_completed
    ON live_positions(account_pacifica, account_hyperliquid, completed_at DESC, id DESC)
    WHERE account_bindings_key = '' AND state = 'closed' AND completed_at IS NOT NULL;

-- +goose Down
DROP INDEX idx_live_positions_legacy_completed;
DROP INDEX idx_live_positions_legacy_opened;
DROP INDEX idx_live_positions_bindings_completed;
DROP INDEX idx_live_positions_bindings_opened;
