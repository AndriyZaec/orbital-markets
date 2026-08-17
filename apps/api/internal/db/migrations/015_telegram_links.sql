-- +goose Up
CREATE TABLE telegram_link_intents (
    token_hash TEXT PRIMARY KEY,
    account_pacifica TEXT NOT NULL,
    account_hyperliquid TEXT NOT NULL,
    expires_at INTEGER NOT NULL,
    created_at INTEGER NOT NULL
);

CREATE INDEX idx_telegram_link_intents_expires_at
    ON telegram_link_intents (expires_at);

CREATE TABLE telegram_account_links (
    chat_id INTEGER PRIMARY KEY,
    account_pacifica TEXT NOT NULL,
    account_hyperliquid TEXT NOT NULL,
    linked_at INTEGER NOT NULL
);

-- +goose Down
DROP TABLE telegram_account_links;
DROP INDEX idx_telegram_link_intents_expires_at;
DROP TABLE telegram_link_intents;
