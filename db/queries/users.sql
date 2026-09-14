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
SET name = $2, email = $3, is_admin = $4
WHERE id = $1
RETURNING *;

-- name: UpdateUserPassword :exec
UPDATE users
SET password_hash = $2
WHERE id = $1;

-- name: ListUsers :many
SELECT * FROM users ORDER BY name, id;

-- name: LockAdministrators :many
SELECT * FROM users WHERE is_admin = true ORDER BY id FOR UPDATE;

-- name: GetUserByPublicIDForUpdate :one
SELECT * FROM users WHERE public_id = $1 FOR UPDATE;
