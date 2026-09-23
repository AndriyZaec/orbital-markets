-- +goose Up
CREATE TABLE aster_data_agents (
    probe_id TEXT PRIMARY KEY,
    owner_account TEXT NOT NULL UNIQUE,
    execution_agent TEXT NOT NULL,
    agent_address TEXT NOT NULL UNIQUE,
    key_version INTEGER NOT NULL,
    key_nonce BLOB NOT NULL,
    key_ciphertext BLOB NOT NULL,
    approval_nonce INTEGER NOT NULL,
    requested_expiry INTEGER NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('pending', 'submitting', 'approved', 'rejected', 'uncertain')),
    approved_at TEXT,
    last_result_json TEXT NOT NULL DEFAULT '',
    last_error TEXT NOT NULL DEFAULT ''
);

-- +goose Down
DROP TABLE aster_data_agents;
