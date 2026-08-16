-- +goose Up
UPDATE live_positions
SET completed_at = NULL
WHERE state IN ('open', 'degraded', 'closing');

-- +goose Down
SELECT 1;
