-- name: GetEncryptionDeployment :one
SELECT public_id FROM encryption_deployment WHERE singleton = true;

-- name: GetUserKeyBundleForUpdate :one
SELECT * FROM user_key_bundles WHERE user_id = $1 FOR UPDATE;

-- name: ListUserEncryptionIdentities :many
SELECT * FROM user_encryption_identities WHERE user_id = $1 ORDER BY generation;

-- name: CreateUserEncryptionIdentity :exec
INSERT INTO user_encryption_identities (
    user_id, generation, signing_public_key, recipient_public_key, recipient_key_id,
    certificate, certificate_signature, continuity_certificate, continuity_signature
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: CreateV2KeyBundle :one
INSERT INTO user_key_bundles (
    user_id, format_version, kdf_salt, encrypted_master_key, recovery_encrypted_master_key,
    private_key_envelope, identity_generation, bundle_revision
) VALUES ($1, 2, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: UpdateV2KeyBundle :one
UPDATE user_key_bundles
SET kdf_salt = sqlc.arg(kdf_salt), encrypted_master_key = sqlc.arg(encrypted_master_key),
    recovery_encrypted_master_key = sqlc.arg(recovery_encrypted_master_key),
    private_key_envelope = sqlc.arg(private_key_envelope), identity_generation = sqlc.arg(identity_generation),
    bundle_revision = sqlc.arg(bundle_revision), updated_at = now()
WHERE user_id = sqlc.arg(user_id) AND format_version = 2
    AND identity_generation = sqlc.arg(expected_generation) AND bundle_revision = sqlc.arg(expected_revision)
RETURNING *;

-- name: GetUserEncryptionIdentity :one
SELECT * FROM user_encryption_identities WHERE user_id = $1 AND generation = $2;
