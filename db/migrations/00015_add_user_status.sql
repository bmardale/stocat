-- +goose Up
ALTER TABLE users ADD COLUMN disabled_at TIMESTAMPTZ;

CREATE INDEX users_active_admin_idx ON users (id)
WHERE is_admin AND disabled_at IS NULL;

-- +goose Down
DROP INDEX users_active_admin_idx;
ALTER TABLE users DROP COLUMN disabled_at;
