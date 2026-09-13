-- name: CreateUserKeyBundle :one
INSERT INTO user_key_bundles (
    user_id, format_version, kdf_salt, kdf_parameters, encrypted_master_key,
    recovery_encrypted_master_key, master_encrypted_recovery_key
) VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetUserKeyBundle :one
SELECT * FROM user_key_bundles WHERE user_id = $1;

-- name: UpdateUserKeyBundlePassword :one
UPDATE user_key_bundles
SET format_version = $2, kdf_salt = $3, kdf_parameters = $4,
    encrypted_master_key = $5, updated_at = now()
WHERE user_id = $1
RETURNING *;
