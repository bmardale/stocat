-- +goose Up
ALTER TABLE user_key_bundles
    ADD COLUMN private_key_envelope BYTEA,
    ADD COLUMN bundle_revision BIGINT NOT NULL DEFAULT 1 CHECK (bundle_revision > 0);

ALTER TABLE libraries
    ADD COLUMN encryption_format TEXT;
UPDATE libraries
SET encryption_format = CASE encryption_mode WHEN 'e2ee' THEN 'v1' ELSE 'plain' END;
ALTER TABLE libraries
    ALTER COLUMN encryption_format SET NOT NULL,
    ALTER COLUMN encryption_format SET DEFAULT 'plain',
    ADD CONSTRAINT libraries_encryption_format_check
        CHECK (encryption_format IN ('plain', 'v1', 'v2')),
    ADD COLUMN encrypted_root_metadata BYTEA,
    ADD COLUMN owner_root_envelope BYTEA,
    ADD COLUMN access_policy_generation BIGINT NOT NULL DEFAULT 1
        CHECK (access_policy_generation > 0);

ALTER TABLE nodes
    ADD COLUMN key_epoch BIGINT NOT NULL DEFAULT 1 CHECK (key_epoch > 0),
    ADD COLUMN metadata_revision BIGINT NOT NULL DEFAULT 1 CHECK (metadata_revision > 0),
    ADD COLUMN encrypted_metadata BYTEA,
    ADD COLUMN metadata_signature BYTEA,
    ADD COLUMN current_version_pointer BYTEA,
    ADD COLUMN current_version_signature BYTEA,
    ADD COLUMN visible_encryption_generation BIGINT NOT NULL DEFAULT 1
        CHECK (visible_encryption_generation > 0),
    ADD CONSTRAINT nodes_encrypted_metadata_size CHECK (
        encrypted_metadata IS NULL OR octet_length(encrypted_metadata) <= 65536
    );

CREATE TABLE user_encryption_identities (
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    generation BIGINT NOT NULL CHECK (generation > 0),
    signing_public_key BYTEA NOT NULL CHECK (octet_length(signing_public_key) = 32),
    recipient_public_key BYTEA NOT NULL CHECK (octet_length(recipient_public_key) = 32),
    recipient_key_id BYTEA NOT NULL CHECK (octet_length(recipient_key_id) = 32),
    certificate BYTEA NOT NULL CHECK (octet_length(certificate) <= 16384),
    continuity_certificate BYTEA CHECK (octet_length(continuity_certificate) <= 16384),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, generation),
    UNIQUE (user_id, recipient_key_id)
);

CREATE TABLE user_encryption_contacts (
    user_id BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    revision BIGINT NOT NULL CHECK (revision > 0),
    encrypted_store BYTEA NOT NULL CHECK (octet_length(encrypted_store) <= 65536),
    signature BYTEA NOT NULL CHECK (octet_length(signature) = 64),
    previous_revision_hash BYTEA CHECK (octet_length(previous_revision_hash) = 32),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE node_key_envelopes (
    child_node_id BIGINT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    parent_node_id BIGINT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    child_epoch BIGINT NOT NULL CHECK (child_epoch > 0),
    parent_epoch BIGINT NOT NULL CHECK (parent_epoch > 0),
    generation BIGINT NOT NULL CHECK (generation > 0),
    ciphertext BYTEA NOT NULL CHECK (octet_length(ciphertext) <= 16384),
    owner_signature BYTEA NOT NULL CHECK (octet_length(owner_signature) = 64),
    PRIMARY KEY (child_node_id, child_epoch, generation),
    CHECK (child_node_id <> parent_node_id)
);

CREATE INDEX node_key_envelopes_parent_idx
    ON node_key_envelopes (parent_node_id, generation, child_node_id);

CREATE TABLE file_version_keys (
    version_id BIGINT PRIMARY KEY REFERENCES file_versions(id) ON DELETE CASCADE,
    node_id BIGINT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    node_epoch BIGINT NOT NULL CHECK (node_epoch > 0),
    generation BIGINT NOT NULL CHECK (generation > 0),
    content_id BYTEA NOT NULL CHECK (octet_length(content_id) = 16),
    encrypted_content_key BYTEA NOT NULL CHECK (octet_length(encrypted_content_key) <= 16384),
    signed_manifest BYTEA NOT NULL CHECK (octet_length(signed_manifest) <= 16384),
    UNIQUE (version_id, node_id),
    FOREIGN KEY (version_id, node_id) REFERENCES file_versions(id, node_id) ON DELETE CASCADE
);

CREATE TABLE shares (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    public_id TEXT NOT NULL UNIQUE,
    owner_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    recipient_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    library_id BIGINT NOT NULL,
    root_node_id BIGINT NOT NULL,
    role TEXT NOT NULL CHECK (role = 'viewer'),
    state TEXT NOT NULL CHECK (state IN ('pending', 'accepted', 'declined', 'left', 'revoked')),
    expires_at TIMESTAMPTZ,
    certificate BYTEA NOT NULL CHECK (octet_length(certificate) <= 16384),
    revision BIGINT NOT NULL CHECK (revision > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (owner_id <> recipient_id),
    FOREIGN KEY (library_id, owner_id) REFERENCES libraries(id, owner_id) ON DELETE CASCADE,
    FOREIGN KEY (root_node_id, library_id) REFERENCES nodes(id, library_id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX shares_active_target_recipient_key
    ON shares (owner_id, root_node_id, recipient_id)
    WHERE state IN ('pending', 'accepted');
CREATE INDEX shares_recipient_inbox_idx ON shares (recipient_id, state, id);
CREATE INDEX shares_owner_idx ON shares (owner_id, id);

CREATE TABLE share_key_envelopes (
    share_id BIGINT NOT NULL REFERENCES shares(id) ON DELETE CASCADE,
    grant_revision BIGINT NOT NULL CHECK (grant_revision > 0),
    recipient_key_id BYTEA NOT NULL CHECK (octet_length(recipient_key_id) = 32),
    target_epoch BIGINT NOT NULL CHECK (target_epoch > 0),
    ciphertext BYTEA NOT NULL CHECK (octet_length(ciphertext) <= 16384),
    encapsulated_key BYTEA NOT NULL CHECK (octet_length(encapsulated_key) = 32),
    signer_record BYTEA NOT NULL CHECK (octet_length(signer_record) <= 16384),
    PRIMARY KEY (share_id, grant_revision)
);

CREATE TABLE public_links (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    public_id TEXT NOT NULL UNIQUE,
    owner_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    library_id BIGINT NOT NULL,
    root_node_id BIGINT NOT NULL,
    token_hash BYTEA NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    expires_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    policy_revision BIGINT NOT NULL CHECK (policy_revision > 0),
    immutable_policy BYTEA NOT NULL CHECK (octet_length(immutable_policy) <= 16384),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (library_id, owner_id) REFERENCES libraries(id, owner_id) ON DELETE CASCADE,
    FOREIGN KEY (root_node_id, library_id) REFERENCES nodes(id, library_id) ON DELETE CASCADE
);

CREATE INDEX public_links_owner_node_idx ON public_links (owner_id, root_node_id, id);

CREATE TABLE public_link_capsules (
    link_id BIGINT PRIMARY KEY REFERENCES public_links(id) ON DELETE CASCADE,
    target_epoch BIGINT NOT NULL CHECK (target_epoch > 0),
    capsule_revision BIGINT NOT NULL CHECK (capsule_revision > 0),
    ciphertext BYTEA NOT NULL CHECK (octet_length(ciphertext) <= 16384),
    nonce BYTEA NOT NULL CHECK (octet_length(nonce) = 24),
    password_profile TEXT,
    password_salt BYTEA,
    authenticated_fields BYTEA NOT NULL CHECK (octet_length(authenticated_fields) <= 16384),
    CHECK ((password_profile IS NULL) = (password_salt IS NULL)),
    CHECK (password_salt IS NULL OR octet_length(password_salt) = 16)
);

CREATE TABLE public_link_management (
    link_id BIGINT PRIMARY KEY REFERENCES public_links(id) ON DELETE CASCADE,
    owner_envelope BYTEA NOT NULL CHECK (octet_length(owner_envelope) <= 16384)
);

CREATE TABLE encryption_rotations (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    public_id TEXT NOT NULL UNIQUE,
    owner_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    library_id BIGINT NOT NULL,
    root_node_id BIGINT NOT NULL,
    barrier_generation BIGINT NOT NULL CHECK (barrier_generation > 0),
    expected_revisions BYTEA NOT NULL CHECK (octet_length(expected_revisions) <= 65536),
    state TEXT NOT NULL CHECK (state IN ('barrier', 'staging', 'committed', 'abandoned')),
    encrypted_resume_state BYTEA CHECK (octet_length(encrypted_resume_state) <= 65536),
    committed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (library_id, owner_id) REFERENCES libraries(id, owner_id) ON DELETE CASCADE,
    FOREIGN KEY (root_node_id, library_id) REFERENCES nodes(id, library_id) ON DELETE CASCADE,
    UNIQUE (id, owner_id),
    CHECK ((state = 'committed') = (committed_at IS NOT NULL))
);

CREATE UNIQUE INDEX encryption_rotations_active_barrier_key
    ON encryption_rotations (library_id, root_node_id)
    WHERE state IN ('barrier', 'staging');

CREATE TABLE encryption_rotation_batches (
    rotation_id BIGINT NOT NULL REFERENCES encryption_rotations(id) ON DELETE CASCADE,
    batch_number BIGINT NOT NULL CHECK (batch_number >= 0),
    record_count INTEGER NOT NULL CHECK (record_count BETWEEN 1 AND 1000),
    records BYTEA NOT NULL,
    records_hash BYTEA NOT NULL CHECK (octet_length(records_hash) = 32),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (rotation_id, batch_number)
);

-- +goose Down
DROP TABLE encryption_rotation_batches;
DROP TABLE encryption_rotations;
DROP TABLE public_link_management;
DROP TABLE public_link_capsules;
DROP TABLE public_links;
DROP TABLE share_key_envelopes;
DROP TABLE shares;
DROP TABLE file_version_keys;
DROP TABLE node_key_envelopes;
DROP TABLE user_encryption_contacts;
DROP TABLE user_encryption_identities;
ALTER TABLE nodes
    DROP CONSTRAINT nodes_encrypted_metadata_size,
    DROP COLUMN visible_encryption_generation,
    DROP COLUMN current_version_signature,
    DROP COLUMN current_version_pointer,
    DROP COLUMN metadata_signature,
    DROP COLUMN encrypted_metadata,
    DROP COLUMN metadata_revision,
    DROP COLUMN key_epoch;
ALTER TABLE libraries
    DROP CONSTRAINT libraries_encryption_format_check,
    DROP COLUMN access_policy_generation,
    DROP COLUMN owner_root_envelope,
    DROP COLUMN encrypted_root_metadata,
    DROP COLUMN encryption_format;
ALTER TABLE user_key_bundles
    DROP COLUMN bundle_revision,
    DROP COLUMN private_key_envelope;
