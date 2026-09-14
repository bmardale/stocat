-- name: EnsurePasskeyUser :one
INSERT INTO passkey_users (user_id, handle)
VALUES ($1, $2)
ON CONFLICT (user_id) DO UPDATE SET user_id = EXCLUDED.user_id
RETURNING handle;

-- name: GetPasskeyUserByHandle :one
SELECT sqlc.embed(users), passkey_users.handle
FROM passkey_users
JOIN users ON users.id = passkey_users.user_id
WHERE passkey_users.handle = $1 AND users.disabled_at IS NULL;

-- name: ListPasskeys :many
SELECT * FROM passkeys
WHERE user_id = sqlc.arg(user_id) AND rp_id = sqlc.arg(rp_id)
ORDER BY created_at DESC, id DESC;

-- name: CreatePasskey :one
INSERT INTO passkeys (
    public_id, user_id, rp_id, credential_id, name, public_key, attestation_type, attestation_format,
    attestation, extensions, transports, aaguid, attachment, sign_count, flags
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
RETURNING *;

-- name: UpdatePasskeyUsage :execrows
UPDATE passkeys
SET sign_count = sqlc.arg(sign_count), flags = sqlc.arg(flags), last_used_at = now()
WHERE rp_id = sqlc.arg(rp_id) AND credential_id = sqlc.arg(credential_id);

-- name: RenamePasskey :one
UPDATE passkeys
SET name = sqlc.arg(name)
WHERE public_id = sqlc.arg(public_id) AND user_id = sqlc.arg(user_id)
RETURNING *;

-- name: DeletePasskey :one
DELETE FROM passkeys WHERE public_id = sqlc.arg(public_id) AND user_id = sqlc.arg(user_id)
RETURNING name;

-- name: DeleteAllUserPasskeys :execrows
DELETE FROM passkeys WHERE user_id = $1;

-- name: DeleteUserPasskeyUser :exec
DELETE FROM passkey_users WHERE user_id = $1;

-- name: CreateWebAuthnCeremony :exec
INSERT INTO webauthn_ceremonies (challenge, kind, user_id, session_data, expires_at)
VALUES ($1, $2, $3, $4, $5);

-- name: ConsumeWebAuthnCeremony :one
DELETE FROM webauthn_ceremonies
WHERE challenge = sqlc.arg(challenge) AND kind = sqlc.arg(kind)
    AND user_id IS NOT DISTINCT FROM sqlc.narg(user_id)::bigint
    AND expires_at > now()
RETURNING session_data;

-- name: DeleteExpiredWebAuthnCeremonies :exec
DELETE FROM webauthn_ceremonies WHERE expires_at <= now();
