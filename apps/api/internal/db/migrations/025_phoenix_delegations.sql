-- +goose Up
-- One current operation per owner; pending sends cannot be overwritten.
CREATE TABLE phoenix_delegations (
    owner_account TEXT PRIMARY KEY,
    request_id TEXT NOT NULL UNIQUE,
    record_json TEXT NOT NULL,
    state TEXT NOT NULL,
    signature TEXT NOT NULL DEFAULT '',
    expires_unix_ns INTEGER NOT NULL
);

-- +goose Down
DROP TABLE phoenix_delegations;
