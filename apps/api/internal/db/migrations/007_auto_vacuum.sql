-- +goose NO TRANSACTION
-- +goose Up
PRAGMA auto_vacuum = INCREMENTAL;
VACUUM;

-- +goose Down
PRAGMA auto_vacuum = NONE;
VACUUM;
