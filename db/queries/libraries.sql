-- name: NextLibraryID :one
SELECT nextval('libraries_id_seq')::bigint;

-- name: NextNodeID :one
SELECT nextval('nodes_id_seq')::bigint;

-- name: CreateLibrary :one
INSERT INTO libraries (id, public_id, owner_id, backend_id, root_node_id, name, encryption_mode, key_envelope)
OVERRIDING SYSTEM VALUE
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
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

-- name: GetNodeByPublicIDAndOwner :one
SELECT n.*
FROM nodes n
JOIN libraries l ON l.id = n.library_id
WHERE n.public_id = $1 AND l.owner_id = $2;

-- name: ListLibrariesForAdmin :many
SELECT id, public_id, owner_id, name, quota_mb
FROM libraries
ORDER BY owner_id, name, id;

-- name: ListLibrariesByOwnerID :many
SELECT id, public_id, owner_id, name, quota_mb
FROM libraries
WHERE owner_id = $1
ORDER BY name, id;

-- name: UpdateLibraryQuota :one
UPDATE libraries
SET quota_mb = sqlc.arg(quota_mb)
WHERE public_id = sqlc.arg(public_id)
RETURNING id, public_id, owner_id, name, quota_mb;

-- name: ListChildNodes :many
SELECT * FROM nodes
WHERE library_id = sqlc.arg(library_id) AND parent_id = sqlc.arg(parent_id)
  AND trashed_at IS NULL AND id > sqlc.arg(after_id)
ORDER BY id
LIMIT sqlc.arg(page_limit);

-- name: ListEnabledStorageBackends :many
SELECT public_id, name, type FROM storage_backends WHERE enabled = true ORDER BY lower(name), id;
