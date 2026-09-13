-- +goose Up
ALTER TABLE users
    ADD COLUMN default_quota_mb BIGINT
    CONSTRAINT users_default_quota_mb_check CHECK (default_quota_mb IS NULL OR default_quota_mb >= 0);

ALTER TABLE libraries
    ADD COLUMN quota_mb BIGINT
    CONSTRAINT libraries_quota_mb_check CHECK (quota_mb IS NULL OR quota_mb >= 0);

-- +goose Down
ALTER TABLE libraries DROP COLUMN quota_mb;
ALTER TABLE users DROP COLUMN default_quota_mb;
