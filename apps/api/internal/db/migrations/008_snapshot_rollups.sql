-- +goose Up
CREATE TABLE market_snapshots_5m (
    venue       TEXT    NOT NULL,
    asset       TEXT    NOT NULL,
    bucket_unix INTEGER NOT NULL,
    open        REAL    NOT NULL,
    high        REAL    NOT NULL,
    low         REAL    NOT NULL,
    close       REAL    NOT NULL,
    funding_avg REAL    NOT NULL,
    oi_avg      REAL    NOT NULL,
    bid_avg     REAL    NOT NULL,
    ask_avg     REAL    NOT NULL,
    PRIMARY KEY (venue, asset, bucket_unix)
);

CREATE INDEX idx_snapshots_5m_asset_bucket ON market_snapshots_5m(asset, bucket_unix);

CREATE TABLE market_snapshots_1h (
    venue       TEXT    NOT NULL,
    asset       TEXT    NOT NULL,
    bucket_unix INTEGER NOT NULL,
    open        REAL    NOT NULL,
    high        REAL    NOT NULL,
    low         REAL    NOT NULL,
    close       REAL    NOT NULL,
    funding_avg REAL    NOT NULL,
    oi_avg      REAL    NOT NULL,
    bid_avg     REAL    NOT NULL,
    ask_avg     REAL    NOT NULL,
    PRIMARY KEY (venue, asset, bucket_unix)
);

CREATE INDEX idx_snapshots_1h_asset_bucket ON market_snapshots_1h(asset, bucket_unix);

-- +goose Down
DROP TABLE market_snapshots_1h;
DROP TABLE market_snapshots_5m;
