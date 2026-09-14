-- name: CreateUser :one
INSERT INTO users (public_id, name, email, password_hash)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1;

-- name: GetUserByPublicID :one
SELECT * FROM users WHERE public_id = $1;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE lower(email) = lower(sqlc.arg(email)::text);

-- name: SetUserAdminByEmail :one
UPDATE users SET is_admin = sqlc.arg(is_admin)
WHERE lower(email) = lower(sqlc.arg(email)::text)
RETURNING *;

-- name: GetUserByEmailForUpdate :one
SELECT * FROM users WHERE lower(email) = lower(sqlc.arg(email)::text) FOR UPDATE;

-- name: GetUserByIDForUpdate :one
SELECT * FROM users WHERE id = $1 FOR UPDATE;

-- name: UpdateUserAccount :one
UPDATE users
SET name = $2, email = $3
WHERE id = $1
RETURNING *;

-- name: UpdateAdminUser :one
UPDATE users
SET name = $2, email = $3, is_admin = $4, disabled_at = $5
WHERE id = $1
RETURNING *;

-- name: UpdateUserPassword :exec
UPDATE users
SET password_hash = $2
WHERE id = $1;

-- name: ListUsers :many
SELECT * FROM users ORDER BY name, id;

-- name: ListUsersPage :many
SELECT * FROM users
WHERE (
    sqlc.arg(search)::text = '' OR
    name ILIKE '%' || sqlc.arg(search)::text || '%' OR
    email ILIKE '%' || sqlc.arg(search)::text || '%' OR
    public_id ILIKE '%' || sqlc.arg(search)::text || '%'
)
AND (sqlc.arg(before_id)::bigint = 0 OR id < sqlc.arg(before_id)::bigint)
ORDER BY id DESC
LIMIT sqlc.arg(page_limit);

-- name: ListUserAdminDetails :many
SELECT u.id,
       (
           SELECT coalesce(sum(bl.size_bytes), 0)::bigint
           FROM blobs bl
           JOIN libraries l ON l.id = bl.library_id
           WHERE l.owner_id = u.id
       ) AS storage_used_bytes,
       (
           SELECT coalesce(sum(us.declared_size), 0)::bigint
           FROM upload_sessions us
           WHERE us.owner_id = u.id
             AND us.state IN ('created', 'uploading', 'uploaded', 'finalizing', 'failed')
       ) AS storage_reserved_bytes,
       nullif(
           greatest(
               coalesce((SELECT max(s.created_at) FROM sessions s WHERE s.user_id = u.id), 'epoch'::timestamptz),
               coalesce((SELECT max(e.created_at) FROM audit_events e WHERE e.actor_id = u.id OR e.subject_id = u.id), 'epoch'::timestamptz)
           ),
           'epoch'::timestamptz
       )::timestamptz AS last_active_at,
       (
           SELECT count(*)::bigint
           FROM sessions s
           WHERE s.user_id = u.id AND s.expires_at > now()
       ) AS active_sessions,
       (
           SELECT count(*)::bigint
           FROM passkeys p
           WHERE p.user_id = u.id
       ) AS passkey_count
FROM users u
WHERE u.id = ANY(sqlc.arg(user_ids)::bigint[]);

-- name: LockActiveAdministrators :many
SELECT * FROM users
WHERE is_admin AND disabled_at IS NULL
ORDER BY id
FOR UPDATE;

-- name: GetUserByPublicIDForUpdate :one
SELECT * FROM users WHERE public_id = $1 FOR UPDATE;
