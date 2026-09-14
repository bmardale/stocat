-- +goose Up
ALTER TABLE sessions ADD COLUMN authenticated_at TIMESTAMPTZ;
UPDATE sessions SET authenticated_at = created_at;
ALTER TABLE sessions ALTER COLUMN authenticated_at SET NOT NULL;

-- +goose Down
ALTER TABLE sessions DROP COLUMN authenticated_at;
