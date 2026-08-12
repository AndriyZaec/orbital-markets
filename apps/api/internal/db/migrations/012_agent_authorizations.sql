-- +goose Up
CREATE TABLE agent_authorizations (
    venue TEXT NOT NULL,
    owner_account TEXT NOT NULL,
    agent_address TEXT NOT NULL,
    authorized_at TEXT NOT NULL,
    PRIMARY KEY (venue, owner_account)
);

-- +goose Down
DROP TABLE agent_authorizations;
