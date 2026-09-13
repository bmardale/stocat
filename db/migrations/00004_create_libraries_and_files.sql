-- +goose Up
CREATE TABLE user_key_bundles (
    user_id BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    format_version INTEGER NOT NULL CHECK (format_version > 0),
    kdf_salt BYTEA NOT NULL CHECK (octet_length(kdf_salt) >= 16),
    kdf_parameters JSONB NOT NULL,
    encrypted_master_key BYTEA NOT NULL,
    recovery_encrypted_master_key BYTEA NOT NULL,
    master_encrypted_recovery_key BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE libraries (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    public_id TEXT NOT NULL UNIQUE,
    owner_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    backend_id BIGINT NOT NULL REFERENCES storage_backends(id) ON DELETE RESTRICT,
    root_node_id BIGINT NOT NULL,
    name TEXT NOT NULL,
    encryption_mode TEXT NOT NULL CHECK (encryption_mode IN ('none', 'e2ee')),
    key_envelope BYTEA,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT libraries_encryption_key_check CHECK (
        (encryption_mode = 'none' AND key_envelope IS NULL) OR
        (encryption_mode = 'e2ee' AND key_envelope IS NOT NULL AND octet_length(key_envelope) > 0)
    ),
    UNIQUE (owner_id, name),
    UNIQUE (id, owner_id),
    UNIQUE (id, backend_id),
    UNIQUE (root_node_id)
);

CREATE TABLE nodes (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    public_id TEXT NOT NULL UNIQUE,
    library_id BIGINT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    parent_id BIGINT REFERENCES nodes(id) ON DELETE RESTRICT,
    kind TEXT NOT NULL CHECK (kind IN ('file', 'folder')),
    name TEXT,
    encrypted_name BYTEA,
    name_token BYTEA,
    current_version_id BIGINT,
    revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0),
    trashed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT nodes_name_check CHECK (
        (parent_id IS NULL AND name IS NULL AND encrypted_name IS NULL AND name_token IS NULL) OR
        (parent_id IS NOT NULL AND name IS NOT NULL AND encrypted_name IS NULL AND name_token IS NULL) OR
        (parent_id IS NOT NULL AND name IS NULL AND encrypted_name IS NOT NULL AND name_token IS NOT NULL)
    ),
    CONSTRAINT nodes_file_version_check CHECK (kind = 'file' OR current_version_id IS NULL),
    UNIQUE (id, library_id)
);

ALTER TABLE libraries
    ADD CONSTRAINT libraries_root_node_fkey
    FOREIGN KEY (root_node_id, id) REFERENCES nodes(id, library_id)
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE nodes
    ADD CONSTRAINT nodes_parent_library_fkey
    FOREIGN KEY (parent_id, library_id) REFERENCES nodes(id, library_id);

CREATE UNIQUE INDEX nodes_library_root_key ON nodes (library_id) WHERE parent_id IS NULL;
CREATE UNIQUE INDEX nodes_plain_active_name_key
    ON nodes (parent_id, name) WHERE trashed_at IS NULL AND name IS NOT NULL;
CREATE UNIQUE INDEX nodes_encrypted_active_name_key
    ON nodes (parent_id, name_token) WHERE trashed_at IS NULL AND name_token IS NOT NULL;
CREATE INDEX nodes_parent_id_idx ON nodes (parent_id, id);
CREATE INDEX nodes_library_id_idx ON nodes (library_id, id);
CREATE INDEX nodes_trashed_at_idx ON nodes (trashed_at) WHERE trashed_at IS NOT NULL;

CREATE TABLE blobs (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    public_id TEXT NOT NULL UNIQUE,
    library_id BIGINT NOT NULL REFERENCES libraries(id) ON DELETE RESTRICT,
    size_bytes BIGINT NOT NULL CHECK (size_bytes >= 0),
    ciphertext_sha256 BYTEA NOT NULL CHECK (octet_length(ciphertext_sha256) = 32),
    dedup_fingerprint BYTEA NOT NULL CHECK (octet_length(dedup_fingerprint) = 32),
    encryption_format TEXT,
    encrypted_file_key BYTEA,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT blobs_encryption_check CHECK (
        (encryption_format IS NULL AND encrypted_file_key IS NULL) OR
        (encryption_format IS NOT NULL AND encrypted_file_key IS NOT NULL AND octet_length(encrypted_file_key) > 0)
    ),
    UNIQUE (library_id, dedup_fingerprint),
    UNIQUE (id, library_id)
);

CREATE TABLE blob_locations (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    public_id TEXT NOT NULL UNIQUE,
    blob_id BIGINT NOT NULL REFERENCES blobs(id) ON DELETE RESTRICT,
    library_id BIGINT NOT NULL,
    backend_id BIGINT NOT NULL REFERENCES storage_backends(id) ON DELETE RESTRICT,
    object_key TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('pending', 'available', 'deleting', 'failed')),
    verified_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (backend_id, object_key),
    UNIQUE (blob_id, backend_id),
    FOREIGN KEY (blob_id, library_id) REFERENCES blobs(id, library_id) ON DELETE RESTRICT,
    FOREIGN KEY (library_id, backend_id) REFERENCES libraries(id, backend_id) ON DELETE RESTRICT
);

CREATE TABLE file_versions (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    public_id TEXT NOT NULL UNIQUE,
    node_id BIGINT NOT NULL,
    library_id BIGINT NOT NULL,
    ordinal BIGINT NOT NULL CHECK (ordinal > 0),
    blob_id BIGINT NOT NULL,
    size_bytes BIGINT NOT NULL CHECK (size_bytes >= 0),
    content_sha256 BYTEA CHECK (content_sha256 IS NULL OR octet_length(content_sha256) = 32),
    restored_from_version_id BIGINT REFERENCES file_versions(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (node_id, ordinal),
    UNIQUE (id, node_id),
    FOREIGN KEY (node_id, library_id) REFERENCES nodes(id, library_id) ON DELETE RESTRICT,
    FOREIGN KEY (blob_id, library_id) REFERENCES blobs(id, library_id) ON DELETE RESTRICT
);

ALTER TABLE nodes
    ADD CONSTRAINT nodes_current_version_fkey
    FOREIGN KEY (current_version_id, id) REFERENCES file_versions(id, node_id) ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

CREATE INDEX file_versions_blob_id_idx ON file_versions (blob_id);
CREATE INDEX blob_locations_state_idx ON blob_locations (state, id);

-- +goose Down
ALTER TABLE nodes DROP CONSTRAINT nodes_current_version_fkey;
DROP TABLE file_versions;
DROP TABLE blob_locations;
DROP TABLE blobs;
ALTER TABLE libraries DROP CONSTRAINT libraries_root_node_fkey;
DROP TABLE nodes;
DROP TABLE libraries;
DROP TABLE user_key_bundles;
