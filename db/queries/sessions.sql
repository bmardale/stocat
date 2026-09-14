-- name: CreateSession :exec
INSERT INTO sessions (token_hash, public_id, user_id, user_agent, ip_address, expires_at, authenticated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: GetSession :one
SELECT * FROM sessions WHERE token_hash = $1;

-- name: GetSessionUser :one
SELECT users.* FROM sessions
JOIN users ON users.id = sessions.user_id
WHERE sessions.token_hash = $1 AND sessions.expires_at > now();

-- name: ListUserSessions :many
SELECT
    public_id,
    user_agent,
    ip_address,
    created_at,
    (token_hash = sqlc.arg(current_token_hash)::bytea)::boolean AS current
FROM sessions
WHERE user_id = sqlc.arg(user_id) AND expires_at > now()
ORDER BY current DESC, created_at DESC;

-- name: DeleteSession :exec
DELETE FROM sessions WHERE token_hash = $1;

-- name: DeleteUserSession :one
DELETE FROM sessions WHERE public_id = $1 AND user_id = $2
RETURNING token_hash;

-- name: DeleteOtherUserSessions :exec
DELETE FROM sessions WHERE user_id = sqlc.arg(user_id) AND token_hash <> sqlc.arg(current_token_hash);

-- name: RevokeSession :one
DELETE FROM sessions WHERE token_hash = $1
RETURNING user_id, public_id;

-- name: GetSessionAuthenticatedAt :one
SELECT authenticated_at FROM sessions WHERE token_hash = $1 AND expires_at > now();

-- name: RefreshSessionAuthentication :execrows
UPDATE sessions SET authenticated_at = now()
WHERE token_hash = $1 AND user_id = $2 AND expires_at > now();
