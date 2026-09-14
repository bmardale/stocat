-- name: GetQuotaSettings :one
SELECT default_limit_bytes FROM quota_settings WHERE id;

-- name: UpdateQuotaSettings :one
UPDATE quota_settings
SET default_limit_bytes = sqlc.narg(default_limit_bytes)
WHERE id
RETURNING default_limit_bytes;

-- name: ListUserDefaultQuotas :many
SELECT user_id, limit_bytes FROM user_default_quotas;

-- name: ListUserBackendQuotas :many
SELECT q.user_id, b.public_id AS backend_public_id, q.limit_bytes
FROM user_backend_quotas q
JOIN storage_backends b ON b.id = q.backend_id
ORDER BY q.user_id, lower(b.name), b.id;

-- name: DeleteUserDefaultQuota :exec
DELETE FROM user_default_quotas WHERE user_id = $1;

-- name: CreateUserDefaultQuota :exec
INSERT INTO user_default_quotas (user_id, limit_bytes)
VALUES (sqlc.arg(user_id), sqlc.narg(limit_bytes));

-- name: DeleteUserBackendQuotas :exec
DELETE FROM user_backend_quotas WHERE user_id = $1;

-- name: CreateUserBackendQuota :execrows
INSERT INTO user_backend_quotas (user_id, backend_id, limit_bytes)
SELECT sqlc.arg(user_id), b.id, sqlc.narg(limit_bytes)
FROM storage_backends b
WHERE b.public_id = sqlc.arg(backend_public_id);

-- name: GetUserBackendQuota :one
SELECT s.default_limit_bytes AS global_limit_bytes,
       (d.user_id IS NOT NULL)::boolean AS has_user_default, d.limit_bytes AS user_limit_bytes,
       (o.user_id IS NOT NULL)::boolean AS has_backend_override, o.limit_bytes AS backend_limit_bytes,
       (
           SELECT coalesce(sum(bl.size_bytes), 0)
           FROM blobs bl
           JOIN libraries l ON l.id = bl.library_id
           WHERE l.owner_id = sqlc.arg(user_id) AND l.backend_id = sqlc.arg(backend_id)
       )::bigint AS stored_bytes,
       (
           SELECT coalesce(sum(u.declared_size), 0)
           FROM upload_sessions u
           JOIN libraries l ON l.id = u.library_id
           WHERE u.owner_id = sqlc.arg(user_id) AND l.backend_id = sqlc.arg(backend_id)
             AND u.state IN ('created', 'uploading', 'uploaded', 'finalizing', 'failed')
       )::bigint AS reserved_bytes
FROM quota_settings s
LEFT JOIN user_default_quotas d ON d.user_id = sqlc.arg(user_id)
LEFT JOIN user_backend_quotas o ON o.user_id = sqlc.arg(user_id) AND o.backend_id = sqlc.arg(backend_id)
WHERE s.id;

-- name: ListUserStorageUsage :many
SELECT b.public_id, b.name, b.type,
       s.default_limit_bytes AS global_limit_bytes,
       (d.user_id IS NOT NULL)::boolean AS has_user_default, d.limit_bytes AS user_limit_bytes,
       (o.user_id IS NOT NULL)::boolean AS has_backend_override, o.limit_bytes AS backend_limit_bytes,
       (
           SELECT coalesce(sum(bl.size_bytes), 0)
           FROM blobs bl
           JOIN libraries l ON l.id = bl.library_id
           WHERE l.owner_id = sqlc.arg(user_id) AND l.backend_id = b.id
       )::bigint AS stored_bytes,
       (
           SELECT coalesce(sum(u.declared_size), 0)
           FROM upload_sessions u
           JOIN libraries l ON l.id = u.library_id
           WHERE u.owner_id = sqlc.arg(user_id) AND l.backend_id = b.id
             AND u.state IN ('created', 'uploading', 'uploaded', 'finalizing', 'failed')
       )::bigint AS reserved_bytes
FROM storage_backends b
CROSS JOIN quota_settings s
LEFT JOIN user_default_quotas d ON d.user_id = sqlc.arg(user_id)
LEFT JOIN user_backend_quotas o ON o.user_id = sqlc.arg(user_id) AND o.backend_id = b.id
WHERE s.id AND EXISTS (SELECT 1 FROM libraries l WHERE l.owner_id = sqlc.arg(user_id) AND l.backend_id = b.id)
ORDER BY lower(b.name), b.id;
