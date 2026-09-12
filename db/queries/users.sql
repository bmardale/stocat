-- name: CreateUser :one
INSERT INTO users (public_id, name, email, password_hash)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1;

-- name: GetUserByPublicID :one
SELECT * FROM users WHERE public_id = $1;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = $1;
