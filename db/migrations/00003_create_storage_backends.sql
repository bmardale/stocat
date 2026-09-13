-- +goose Up
CREATE TABLE storage_backends (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    public_id TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL,
    type TEXT NOT NULL CHECK (type IN ('local', 's3')),
    config JSONB NOT NULL,
    -- The application encrypts this JSON object with APP_KEY. PostgreSQL never sees the plaintext.
    encrypted_secrets TEXT NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX storage_backends_name_lower_key ON storage_backends (lower(name));

-- +goose Down
DROP TABLE storage_backends;
