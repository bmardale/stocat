-- name: NextLibraryID :one
SELECT nextval('libraries_id_seq')::bigint;

-- name: NextNodeID :one
SELECT nextval('nodes_id_seq')::bigint;

-- name: CreateLibrary :one
INSERT INTO libraries (
    id, public_id, owner_id, backend_id, root_node_id, name, encryption_mode,
    key_envelope, encryption_format
)
OVERRIDING SYSTEM VALUE
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, CASE $7::text WHEN 'e2ee' THEN 'v1' ELSE 'plain' END)
RETURNING *;

-- name: CreateNodeWithID :one
INSERT INTO nodes (id, public_id, library_id, parent_id, kind, name, encrypted_name, name_token)
OVERRIDING SYSTEM VALUE
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: CreateNode :one
INSERT INTO nodes (public_id, library_id, parent_id, kind, name, encrypted_name, name_token)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: ListLibrariesByOwner :many
SELECT l.*, b.public_id AS backend_public_id, b.name AS backend_name, b.type AS backend_type,
       root.public_id AS root_node_public_id
FROM libraries l
JOIN storage_backends b ON b.id = l.backend_id
JOIN nodes root ON root.id = l.root_node_id
WHERE l.owner_id = $1
ORDER BY l.name, l.id;

-- name: GetLibraryByPublicIDAndOwner :one
SELECT l.*, b.public_id AS backend_public_id, b.name AS backend_name, b.type AS backend_type,
       root.public_id AS root_node_public_id
FROM libraries l
JOIN storage_backends b ON b.id = l.backend_id
JOIN nodes root ON root.id = l.root_node_id
WHERE l.public_id = $1 AND l.owner_id = $2;

-- name: GetEnabledBackendByPublicID :one
SELECT * FROM storage_backends WHERE public_id = $1 AND enabled = true;

-- name: RenameLibrary :one
UPDATE libraries
SET name = $3, updated_at = now()
WHERE public_id = $1 AND owner_id = $2
RETURNING *;

-- name: DeleteEmptyLibrary :execrows
WITH candidate AS (
    SELECT l.id
    FROM libraries l
    WHERE l.public_id = $1 AND l.owner_id = $2
      AND NOT EXISTS (SELECT 1 FROM nodes n WHERE n.library_id = l.id AND n.kind = 'file')
), removed_uploads AS (
    DELETE FROM upload_sessions WHERE library_id IN (SELECT id FROM candidate)
)
DELETE FROM libraries WHERE id IN (SELECT id FROM candidate);

-- name: GetNodeByPublicIDAndOwner :one
SELECT n.*
FROM nodes n
JOIN libraries l ON l.id = n.library_id
WHERE n.public_id = $1 AND l.owner_id = $2;

-- name: ListChildNodes :many
SELECT n.* FROM nodes n
WHERE n.library_id = sqlc.arg(library_id) AND n.parent_id = sqlc.arg(parent_id)
  AND n.trashed_at IS NULL AND n.id > sqlc.arg(after_id)
  AND (sqlc.arg(tag_id)::bigint = 0 OR EXISTS (
      SELECT 1 FROM file_tags ft WHERE ft.node_id = n.id AND ft.tag_id = sqlc.arg(tag_id)
  ))
ORDER BY n.id
LIMIT sqlc.arg(page_limit);

-- name: ListEnabledStorageBackends :many
SELECT public_id, name, type FROM storage_backends WHERE enabled = true ORDER BY lower(name), id;
