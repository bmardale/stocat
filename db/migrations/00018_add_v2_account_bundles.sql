-- +goose Up
ALTER TABLE user_encryption_identities
    ADD COLUMN certificate_signature BYTEA NOT NULL CHECK (octet_length(certificate_signature) = 64),
    ADD COLUMN continuity_signature BYTEA CHECK (octet_length(continuity_signature) = 64),
    ADD CONSTRAINT user_encryption_identities_continuity_check CHECK (
        (continuity_certificate IS NULL) = (continuity_signature IS NULL)
        AND (generation > 1 OR continuity_certificate IS NULL)
    );

ALTER TABLE user_key_bundles
    ALTER COLUMN kdf_parameters DROP NOT NULL,
    ALTER COLUMN master_encrypted_recovery_key DROP NOT NULL,
    ADD COLUMN identity_generation BIGINT,
    ADD CONSTRAINT user_key_bundles_format_check CHECK (
        (format_version <> 2 AND kdf_parameters IS NOT NULL AND master_encrypted_recovery_key IS NOT NULL
            AND private_key_envelope IS NULL AND identity_generation IS NULL) OR
        (format_version = 2 AND kdf_parameters IS NULL AND master_encrypted_recovery_key IS NULL
            AND octet_length(kdf_salt) = 16
            AND octet_length(encrypted_master_key) <= 16384
            AND octet_length(recovery_encrypted_master_key) <= 16384
            AND private_key_envelope IS NOT NULL AND octet_length(private_key_envelope) <= 16384
            AND identity_generation IS NOT NULL)
    ),
    ADD CONSTRAINT user_key_bundles_identity_fkey
        FOREIGN KEY (user_id, identity_generation) REFERENCES user_encryption_identities (user_id, generation);

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM user_key_bundles WHERE format_version = 2)
        OR EXISTS (SELECT 1 FROM user_encryption_identities)
    THEN
        RAISE EXCEPTION 'V2 account bundles prevent rollback. Restore a pre-v2 backup to use the legacy schema.';
    END IF;
END;
$$;
-- +goose StatementEnd
ALTER TABLE user_key_bundles
    DROP CONSTRAINT user_key_bundles_identity_fkey,
    DROP CONSTRAINT user_key_bundles_format_check,
    DROP COLUMN identity_generation,
    ALTER COLUMN master_encrypted_recovery_key SET NOT NULL,
    ALTER COLUMN kdf_parameters SET NOT NULL;
ALTER TABLE user_encryption_identities
    DROP CONSTRAINT user_encryption_identities_continuity_check,
    DROP COLUMN continuity_signature,
    DROP COLUMN certificate_signature;
