-- name: ListTagsByLibrary :many
SELECT * FROM tags WHERE library_id = $1 ORDER BY id;

-- name: CreateTag :one
INSERT INTO tags (public_id, library_id, name, encrypted_name, name_token, color, encrypted_color)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetTagByPublicIDAndOwner :one
SELECT t.*
FROM tags t
JOIN libraries l ON l.id = t.library_id
WHERE t.public_id = $1 AND l.owner_id = $2;

-- name: UpdateTag :one
UPDATE tags
SET name = $2, encrypted_name = $3, name_token = $4, color = $5, encrypted_color = $6, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteTag :execrows
DELETE FROM tags WHERE id = $1;

-- name: AddFileTag :execrows
INSERT INTO file_tags (node_id, tag_id, library_id)
VALUES ($1, $2, $3)
ON CONFLICT DO NOTHING;

-- name: RemoveFileTag :execrows
DELETE FROM file_tags WHERE node_id = $1 AND tag_id = $2;

-- name: ListTagsForNodes :many
SELECT ft.node_id, t.*
FROM file_tags ft
JOIN tags t ON t.id = ft.tag_id
WHERE ft.node_id = ANY(sqlc.arg(node_ids)::bigint[])
ORDER BY ft.node_id, t.id;
