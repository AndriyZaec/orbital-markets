-- +goose Up
CREATE TABLE phoenix_order_attempts (
    request_id TEXT PRIMARY KEY,
    owner_account TEXT NOT NULL,
    client_order_id TEXT NOT NULL,
    signature TEXT NOT NULL UNIQUE,
    request_json TEXT NOT NULL,
    request_digest TEXT NOT NULL,
    before_json TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('pending','submitted','failed','filled','no_fill')),
    error TEXT NOT NULL DEFAULT '',
    fill_json TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE(owner_account, client_order_id)
);
CREATE INDEX phoenix_order_attempts_owner_state ON phoenix_order_attempts(owner_account, state);

-- +goose Down
DROP TABLE phoenix_order_attempts;
