-- +goose Up
CREATE TABLE encryption_deployment (
    singleton BOOLEAN PRIMARY KEY DEFAULT true CHECK (singleton),
    public_id UUID NOT NULL DEFAULT gen_random_uuid() UNIQUE
);
INSERT INTO encryption_deployment DEFAULT VALUES;

-- +goose StatementBegin
CREATE FUNCTION protect_encryption_deployment() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'The encryption deployment identifier is immutable.';
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
CREATE TRIGGER encryption_deployment_immutable
    BEFORE UPDATE OR DELETE OR TRUNCATE ON encryption_deployment
    FOR EACH STATEMENT EXECUTE FUNCTION protect_encryption_deployment();

ALTER TABLE libraries
    DROP CONSTRAINT libraries_owner_id_name_key,
    DROP CONSTRAINT libraries_encryption_key_check,
    ALTER COLUMN name DROP NOT NULL,
    ADD CONSTRAINT libraries_encryption_key_check CHECK (
        (encryption_format = 'plain' AND encryption_mode = 'none' AND name IS NOT NULL
            AND key_envelope IS NULL AND encrypted_root_metadata IS NULL AND owner_root_envelope IS NULL) OR
        (encryption_format = 'v1' AND encryption_mode = 'e2ee' AND name IS NOT NULL
            AND key_envelope IS NOT NULL AND octet_length(key_envelope) > 0
            AND encrypted_root_metadata IS NULL AND owner_root_envelope IS NULL) OR
        (encryption_format = 'v2' AND encryption_mode = 'e2ee' AND name IS NULL AND key_envelope IS NULL
            AND encrypted_root_metadata IS NOT NULL AND owner_root_envelope IS NOT NULL)
    ),
    ADD CONSTRAINT libraries_root_metadata_size CHECK (octet_length(encrypted_root_metadata) BETWEEN 16 AND 65536),
    ADD CONSTRAINT libraries_root_envelope_size CHECK (octet_length(owner_root_envelope) BETWEEN 16 AND 16384);
CREATE UNIQUE INDEX libraries_owner_id_name_key ON libraries (owner_id, name)
    WHERE encryption_format <> 'v2';

ALTER TABLE nodes
    DROP CONSTRAINT nodes_name_check,
    ADD CONSTRAINT nodes_name_check CHECK (
        (parent_id IS NULL AND name IS NULL AND encrypted_name IS NULL AND name_token IS NULL) OR
        (parent_id IS NOT NULL AND encrypted_metadata IS NULL AND (
            (name IS NOT NULL AND encrypted_name IS NULL AND name_token IS NULL) OR
            (name IS NULL AND encrypted_name IS NOT NULL AND name_token IS NOT NULL)
        )) OR
        (parent_id IS NOT NULL AND encrypted_metadata IS NOT NULL AND name IS NULL
            AND encrypted_name IS NULL AND name_token IS NOT NULL AND octet_length(name_token) = 32)
    );

ALTER TABLE blobs
    ALTER COLUMN dedup_fingerprint DROP NOT NULL,
    DROP CONSTRAINT blobs_encryption_check,
    ADD CONSTRAINT blobs_encryption_check CHECK (
        (encryption_format IS NOT DISTINCT FROM 'stocat-framed-v2' AND dedup_fingerprint IS NULL AND encrypted_file_key IS NULL) OR
        (encryption_format IS DISTINCT FROM 'stocat-framed-v2' AND dedup_fingerprint IS NOT NULL AND (
            (encryption_format IS NULL AND encrypted_file_key IS NULL) OR
            (encryption_format IS NOT NULL AND encrypted_file_key IS NOT NULL AND octet_length(encrypted_file_key) > 0)
        ))
    );

ALTER TABLE node_key_envelopes ADD COLUMN library_id BIGINT;
UPDATE node_key_envelopes e SET library_id = n.library_id FROM nodes n WHERE n.id = e.child_node_id;
ALTER TABLE node_key_envelopes
    ALTER COLUMN library_id SET NOT NULL,
    ADD CONSTRAINT node_key_envelopes_child_library_fkey
        FOREIGN KEY (child_node_id, library_id) REFERENCES nodes(id, library_id) ON DELETE CASCADE,
    ADD CONSTRAINT node_key_envelopes_parent_library_fkey
        FOREIGN KEY (parent_node_id, library_id) REFERENCES nodes(id, library_id) ON DELETE CASCADE;

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM user_encryption_identities)
        OR EXISTS (SELECT 1 FROM user_encryption_contacts)
        OR EXISTS (SELECT 1 FROM user_key_bundles WHERE format_version = 2)
        OR EXISTS (SELECT 1 FROM libraries WHERE encryption_format = 'v2')
        OR EXISTS (SELECT 1 FROM blobs WHERE encryption_format = 'stocat-framed-v2')
        OR EXISTS (SELECT 1 FROM nodes WHERE encrypted_metadata IS NOT NULL AND parent_id IS NOT NULL)
    THEN
        RAISE EXCEPTION 'V2 data prevents rollback. Restore a pre-v2 backup to use the legacy schema.';
    END IF;
END;
$$;
-- +goose StatementEnd
ALTER TABLE node_key_envelopes
    DROP CONSTRAINT node_key_envelopes_parent_library_fkey,
    DROP CONSTRAINT node_key_envelopes_child_library_fkey,
    DROP COLUMN library_id;
ALTER TABLE blobs
    DROP CONSTRAINT blobs_encryption_check,
    ALTER COLUMN dedup_fingerprint SET NOT NULL,
    ADD CONSTRAINT blobs_encryption_check CHECK (
        (encryption_format IS NULL AND encrypted_file_key IS NULL) OR
        (encryption_format IS NOT NULL AND encrypted_file_key IS NOT NULL AND octet_length(encrypted_file_key) > 0)
    );
ALTER TABLE nodes
    DROP CONSTRAINT nodes_name_check,
    ADD CONSTRAINT nodes_name_check CHECK (
        (parent_id IS NULL AND name IS NULL AND encrypted_name IS NULL AND name_token IS NULL) OR
        (parent_id IS NOT NULL AND name IS NOT NULL AND encrypted_name IS NULL AND name_token IS NULL) OR
        (parent_id IS NOT NULL AND name IS NULL AND encrypted_name IS NOT NULL AND name_token IS NOT NULL)
    );
DROP INDEX libraries_owner_id_name_key;
ALTER TABLE libraries
    DROP CONSTRAINT libraries_encryption_key_check,
    DROP CONSTRAINT libraries_root_metadata_size,
    DROP CONSTRAINT libraries_root_envelope_size,
    ALTER COLUMN name SET NOT NULL,
    ADD CONSTRAINT libraries_owner_id_name_key UNIQUE (owner_id, name),
    ADD CONSTRAINT libraries_encryption_key_check CHECK (
        (encryption_mode = 'none' AND key_envelope IS NULL) OR
        (encryption_mode = 'e2ee' AND key_envelope IS NOT NULL AND octet_length(key_envelope) > 0)
    );
DROP TABLE encryption_deployment;
DROP FUNCTION protect_encryption_deployment();
