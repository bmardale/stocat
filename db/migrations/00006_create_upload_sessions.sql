-- +goose Up
CREATE TABLE upload_sessions (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    public_id TEXT NOT NULL UNIQUE,
    owner_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    library_id BIGINT NOT NULL REFERENCES libraries(id) ON DELETE RESTRICT,
    parent_id BIGINT NOT NULL,
    target_node_id BIGINT,
    expected_revision BIGINT CHECK (expected_revision IS NULL OR expected_revision > 0),
    name TEXT,
    encrypted_name BYTEA,
    name_token BYTEA,
    declared_size BIGINT NOT NULL CHECK (declared_size >= 0),
    upload_offset BIGINT NOT NULL DEFAULT 0 CHECK (upload_offset >= 0 AND upload_offset <= declared_size),
    staging_key TEXT NOT NULL UNIQUE,
    destination_key TEXT NOT NULL UNIQUE,
    state TEXT NOT NULL CHECK (state IN (
        'created', 'uploading', 'uploaded', 'finalizing', 'completed',
        'conflict', 'failed', 'cancelled', 'expired'
    )),
    dedup_fingerprint BYTEA CHECK (dedup_fingerprint IS NULL OR octet_length(dedup_fingerprint) = 32),
    encryption_format TEXT,
    encrypted_file_key BYTEA,
    published_node_id BIGINT,
    published_version_id BIGINT,
    failure_code TEXT,
    failure_message TEXT,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at TIMESTAMPTZ,
    CONSTRAINT upload_sessions_name_check CHECK (
        (name IS NOT NULL AND encrypted_name IS NULL AND name_token IS NULL) OR
        (name IS NULL AND encrypted_name IS NOT NULL AND name_token IS NOT NULL)
    ),
    CONSTRAINT upload_sessions_encryption_check CHECK (
        (encryption_format IS NULL AND encrypted_file_key IS NULL) OR
        (encryption_format IS NOT NULL AND encrypted_file_key IS NOT NULL AND octet_length(encrypted_file_key) > 0)
    ),
    CONSTRAINT upload_sessions_target_check CHECK (
        (target_node_id IS NULL AND expected_revision IS NULL) OR
        (target_node_id IS NOT NULL AND expected_revision IS NOT NULL)
    ),
    FOREIGN KEY (library_id, owner_id) REFERENCES libraries(id, owner_id) ON DELETE RESTRICT,
    FOREIGN KEY (parent_id, library_id) REFERENCES nodes(id, library_id) ON DELETE RESTRICT,
    FOREIGN KEY (target_node_id, library_id) REFERENCES nodes(id, library_id) ON DELETE RESTRICT,
    FOREIGN KEY (published_node_id, library_id) REFERENCES nodes(id, library_id) ON DELETE RESTRICT,
    FOREIGN KEY (published_version_id, published_node_id)
        REFERENCES file_versions(id, node_id) ON DELETE RESTRICT
);

CREATE INDEX upload_sessions_owner_state_idx ON upload_sessions (owner_id, state, id);
CREATE INDEX upload_sessions_expiry_idx ON upload_sessions (expires_at, id)
    WHERE state IN ('created', 'uploading', 'uploaded', 'failed');

-- +goose Down
DROP TABLE upload_sessions;
