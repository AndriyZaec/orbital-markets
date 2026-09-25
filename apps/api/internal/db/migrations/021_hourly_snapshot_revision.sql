-- +goose Up
CREATE TABLE rollup_revisions (
    name     TEXT    PRIMARY KEY,
    revision INTEGER NOT NULL
);

INSERT INTO rollup_revisions (name, revision)
VALUES (
    'market_snapshots_1h',
    CASE WHEN EXISTS (SELECT 1 FROM market_snapshots_1h) THEN 1 ELSE 0 END
);

-- +goose StatementBegin
CREATE TRIGGER market_snapshots_1h_revision_insert
AFTER INSERT ON market_snapshots_1h
BEGIN
    UPDATE rollup_revisions SET revision = revision + 1 WHERE name = 'market_snapshots_1h';
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER market_snapshots_1h_revision_update
AFTER UPDATE ON market_snapshots_1h
WHEN OLD.venue IS NOT NEW.venue
    OR OLD.asset IS NOT NEW.asset
    OR OLD.bucket_unix IS NOT NEW.bucket_unix
    OR OLD.funding_avg IS NOT NEW.funding_avg
BEGIN
    UPDATE rollup_revisions SET revision = revision + 1 WHERE name = 'market_snapshots_1h';
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER market_snapshots_1h_revision_delete
AFTER DELETE ON market_snapshots_1h
BEGIN
    UPDATE rollup_revisions SET revision = revision + 1 WHERE name = 'market_snapshots_1h';
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER market_snapshots_1h_revision_delete;
DROP TRIGGER market_snapshots_1h_revision_update;
DROP TRIGGER market_snapshots_1h_revision_insert;
DROP TABLE rollup_revisions;
