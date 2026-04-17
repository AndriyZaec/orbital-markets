-- +goose Up
CREATE TABLE market_snapshots (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    venue TEXT NOT NULL,
    asset TEXT NOT NULL,
    market_key TEXT NOT NULL,
    mark_price REAL NOT NULL,
    index_price REAL NOT NULL,
    funding_rate REAL NOT NULL,
    bid_price REAL NOT NULL,
    ask_price REAL NOT NULL,
    open_interest REAL NOT NULL,
    timestamp TEXT NOT NULL
);

CREATE INDEX idx_snapshots_venue_asset_ts ON market_snapshots(venue, asset, timestamp);
CREATE INDEX idx_snapshots_asset_ts ON market_snapshots(asset, timestamp);

-- +goose Down
DROP TABLE market_snapshots;
