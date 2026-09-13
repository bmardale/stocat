-- name: CreateStorageBackend :one
INSERT INTO storage_backends (public_id, name, type, config, encrypted_secrets, enabled)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: ListStorageBackends :many
SELECT * FROM storage_backends ORDER BY lower(name), id;

-- name: GetStorageBackendByPublicID :one
SELECT * FROM storage_backends WHERE public_id = $1;

-- name: LockStorageBackendByPublicID :one
SELECT * FROM storage_backends WHERE public_id = $1 FOR UPDATE;

-- name: UpdateStorageBackend :one
UPDATE storage_backends
SET name = $2, config = $3, encrypted_secrets = $4, enabled = $5, updated_at = now()
WHERE public_id = $1
RETURNING *;

-- name: DeleteStorageBackend :execrows
DELETE FROM storage_backends WHERE public_id = $1;
