-- +goose Up
CREATE TABLE live_funding_venue_sync (
    position_id TEXT NOT NULL REFERENCES live_positions(id),
    venue TEXT NOT NULL,
    synced_at TEXT NOT NULL,
    finalized INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (position_id, venue)
);

CREATE TABLE aster_income_sync (
    account TEXT PRIMARY KEY,
    attempted_at INTEGER NOT NULL
);

-- +goose Down
DROP TABLE aster_income_sync;
DROP TABLE live_funding_venue_sync;
