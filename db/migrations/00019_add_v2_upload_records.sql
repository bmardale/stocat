-- +goose Up
ALTER TABLE file_version_keys
    ADD COLUMN manifest_signature BYTEA NOT NULL CHECK (octet_length(manifest_signature) = 64);

ALTER TABLE nodes
    ADD CONSTRAINT nodes_current_version_record_check
        CHECK ((current_version_pointer IS NULL) = (current_version_signature IS NULL));

ALTER TABLE upload_sessions
    ADD COLUMN node_public_id TEXT,
    ADD COLUMN encrypted_metadata BYTEA CHECK (octet_length(encrypted_metadata) <= 65536),
    ADD COLUMN metadata_signature BYTEA CHECK (octet_length(metadata_signature) = 64),
    ADD COLUMN parent_envelope BYTEA CHECK (octet_length(parent_envelope) <= 16384),
    ADD COLUMN parent_envelope_signature BYTEA CHECK (octet_length(parent_envelope_signature) = 64),
    ADD COLUMN file_key_record BYTEA CHECK (octet_length(file_key_record) <= 16384),
    ADD COLUMN manifest_record BYTEA CHECK (octet_length(manifest_record) <= 16384),
    ADD COLUMN manifest_signature BYTEA CHECK (octet_length(manifest_signature) = 64),
    ADD COLUMN current_version_record BYTEA CHECK (octet_length(current_version_record) <= 16384),
    ADD COLUMN current_version_signature BYTEA CHECK (octet_length(current_version_signature) = 64);

ALTER TABLE upload_sessions
    DROP CONSTRAINT upload_sessions_name_check,
    DROP CONSTRAINT upload_sessions_encryption_check;

-- A v2 session for a new file stores the node records. A v2 replacement stores only its expected revision,
-- because a file deletion can clear the target.
ALTER TABLE upload_sessions
    ADD CONSTRAINT upload_sessions_name_check CHECK (
        (encryption_format IS DISTINCT FROM 'stocat-framed-v2' AND node_public_id IS NULL AND encrypted_metadata IS NULL
            AND metadata_signature IS NULL AND parent_envelope IS NULL AND parent_envelope_signature IS NULL AND (
                (name IS NOT NULL AND encrypted_name IS NULL AND name_token IS NULL) OR
                (name IS NULL AND encrypted_name IS NOT NULL AND name_token IS NOT NULL)
            )) OR
        (encryption_format = 'stocat-framed-v2' AND name IS NULL AND encrypted_name IS NULL AND (
            (node_public_id IS NOT NULL AND target_node_id IS NULL AND expected_revision IS NULL
                AND octet_length(name_token) = 32 AND encrypted_metadata IS NOT NULL AND metadata_signature IS NOT NULL
                AND parent_envelope IS NOT NULL AND parent_envelope_signature IS NOT NULL) OR
            (node_public_id IS NULL AND expected_revision IS NOT NULL AND name_token IS NULL
                AND encrypted_metadata IS NULL AND metadata_signature IS NULL
                AND parent_envelope IS NULL AND parent_envelope_signature IS NULL)
        ))
    ),
    ADD CONSTRAINT upload_sessions_encryption_check CHECK (
        (encryption_format IS NULL AND encrypted_file_key IS NULL) OR
        (encryption_format = 'stocat-framed-v2' AND encrypted_file_key IS NULL AND dedup_fingerprint IS NULL) OR
        (encryption_format <> 'stocat-framed-v2' AND encrypted_file_key IS NOT NULL AND octet_length(encrypted_file_key) > 0)
    ),
    ADD CONSTRAINT upload_sessions_v2_records_check CHECK (
        (file_key_record IS NULL AND manifest_record IS NULL AND manifest_signature IS NULL
            AND current_version_record IS NULL AND current_version_signature IS NULL) OR
        (encryption_format = 'stocat-framed-v2' AND file_key_record IS NOT NULL AND manifest_record IS NOT NULL
            AND manifest_signature IS NOT NULL AND current_version_record IS NOT NULL
            AND current_version_signature IS NOT NULL)
    );

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM upload_sessions WHERE encryption_format = 'stocat-framed-v2')
        OR EXISTS (SELECT 1 FROM file_version_keys)
    THEN
        RAISE EXCEPTION 'V2 uploads prevent rollback. Restore a pre-v2 backup to use the legacy schema.';
    END IF;
END;
$$;
-- +goose StatementEnd
ALTER TABLE upload_sessions
    DROP CONSTRAINT upload_sessions_v2_records_check,
    DROP CONSTRAINT upload_sessions_encryption_check,
    DROP CONSTRAINT upload_sessions_name_check;

ALTER TABLE upload_sessions
    DROP COLUMN current_version_signature,
    DROP COLUMN current_version_record,
    DROP COLUMN manifest_signature,
    DROP COLUMN manifest_record,
    DROP COLUMN file_key_record,
    DROP COLUMN parent_envelope_signature,
    DROP COLUMN parent_envelope,
    DROP COLUMN metadata_signature,
    DROP COLUMN encrypted_metadata,
    DROP COLUMN node_public_id;

ALTER TABLE upload_sessions
    ADD CONSTRAINT upload_sessions_name_check CHECK (
        (name IS NOT NULL AND encrypted_name IS NULL AND name_token IS NULL) OR
        (name IS NULL AND encrypted_name IS NOT NULL AND name_token IS NOT NULL)
    ),
    ADD CONSTRAINT upload_sessions_encryption_check CHECK (
        (encryption_format IS NULL AND encrypted_file_key IS NULL) OR
        (encryption_format IS NOT NULL AND encrypted_file_key IS NOT NULL AND octet_length(encrypted_file_key) > 0)
    );

ALTER TABLE nodes DROP CONSTRAINT nodes_current_version_record_check;
ALTER TABLE file_version_keys DROP COLUMN manifest_signature;
