-- name: CreateInviteCode :one
INSERT INTO invite_codes (public_id, code, created_by)
VALUES ($1, $2, $3)
RETURNING *;

-- name: ListInviteCodes :many
SELECT * FROM invite_codes ORDER BY created_at DESC, id DESC;

-- name: LockUnusedInviteCode :one
SELECT * FROM invite_codes
WHERE lower(code) = lower(sqlc.arg(code)::text) AND used_at IS NULL
FOR UPDATE;

-- name: MarkInviteCodeUsed :exec
UPDATE invite_codes
SET used_by = sqlc.arg(used_by), used_at = now()
WHERE id = sqlc.arg(id);

-- name: DeleteInviteCode :execrows
DELETE FROM invite_codes
WHERE public_id = sqlc.arg(public_id) AND used_at IS NULL;
