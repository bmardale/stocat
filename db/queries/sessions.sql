-- name: CreateSession :exec
INSERT INTO sessions (token_hash, user_id, user_agent, ip_address, expires_at)
VALUES ($1, $2, $3, $4, $5);

-- name: GetSession :one
SELECT * FROM sessions WHERE token_hash = $1;

-- name: GetSessionUser :one
SELECT users.* FROM sessions
JOIN users ON users.id = sessions.user_id
WHERE sessions.token_hash = $1 AND sessions.expires_at > now();

-- name: DeleteSession :exec
DELETE FROM sessions WHERE token_hash = $1;
