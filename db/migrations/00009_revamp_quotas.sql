-- +goose Up
ALTER TABLE libraries DROP COLUMN quota_mb;
ALTER TABLE users DROP COLUMN default_quota_mb;

-- A NULL limit means no limit. A missing user row uses the next level: backend override, user default, global default.
CREATE TABLE quota_settings (
    id BOOLEAN PRIMARY KEY DEFAULT true CHECK (id),
    default_limit_bytes BIGINT CHECK (default_limit_bytes IS NULL OR default_limit_bytes >= 0)
);

INSERT INTO quota_settings (id) VALUES (true);

CREATE TABLE user_default_quotas (
    user_id BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    limit_bytes BIGINT CHECK (limit_bytes IS NULL OR limit_bytes >= 0)
);

CREATE TABLE user_backend_quotas (
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    backend_id BIGINT NOT NULL REFERENCES storage_backends(id) ON DELETE CASCADE,
    limit_bytes BIGINT CHECK (limit_bytes IS NULL OR limit_bytes >= 0),
    PRIMARY KEY (user_id, backend_id)
);

CREATE INDEX user_backend_quotas_backend_id_idx ON user_backend_quotas (backend_id);
CREATE INDEX libraries_owner_backend_idx ON libraries (owner_id, backend_id);

-- +goose Down
DROP INDEX libraries_owner_backend_idx;
DROP TABLE user_backend_quotas;
DROP TABLE user_default_quotas;
DROP TABLE quota_settings;

ALTER TABLE users
    ADD COLUMN default_quota_mb BIGINT
    CONSTRAINT users_default_quota_mb_check CHECK (default_quota_mb IS NULL OR default_quota_mb >= 0);

ALTER TABLE libraries
    ADD COLUMN quota_mb BIGINT
    CONSTRAINT libraries_quota_mb_check CHECK (quota_mb IS NULL OR quota_mb >= 0);
