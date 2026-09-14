-- name: GetCurrentFileByPublicIDAndOwner :one
SELECT
    n.public_id AS node_public_id,
    n.name,
    n.encrypted_name,
    n.revision,
    l.public_id AS library_public_id,
    l.encryption_mode,
    v.public_id AS version_public_id,
    v.size_bytes,
    b.size_bytes AS stored_size_bytes,
    b.ciphertext_sha256,
    b.encryption_format,
    b.encrypted_file_key,
    sb.public_id AS backend_public_id,
    bl.object_key
FROM nodes n
JOIN libraries l ON l.id = n.library_id
JOIN file_versions v ON v.id = n.current_version_id
JOIN blobs b ON b.id = v.blob_id
JOIN blob_locations bl ON bl.blob_id = b.id AND bl.backend_id = l.backend_id AND bl.state = 'available'
JOIN storage_backends sb ON sb.id = bl.backend_id
WHERE n.public_id = sqlc.arg(node_public_id) AND n.kind = 'file' AND n.trashed_at IS NULL
  AND l.owner_id = sqlc.arg(owner_id) AND l.encryption_format <> 'v2';

-- name: GetFileNodeByPublicIDAndOwner :one
SELECT n.id, n.library_id, l.public_id AS library_public_id, l.encryption_mode,
       parent.public_id AS parent_public_id
FROM nodes n
JOIN libraries l ON l.id = n.library_id
JOIN nodes parent ON parent.id = n.parent_id
WHERE n.public_id = sqlc.arg(node_public_id) AND n.kind = 'file' AND n.trashed_at IS NULL
  AND l.owner_id = sqlc.arg(owner_id) AND l.encryption_format <> 'v2';

-- name: GetTrashedFileNodeByPublicIDAndOwner :one
SELECT n.id, n.library_id, n.trashed_at, l.public_id AS library_public_id,
       parent.public_id AS parent_public_id
FROM nodes n
JOIN libraries l ON l.id = n.library_id
JOIN nodes parent ON parent.id = n.parent_id
WHERE n.public_id = sqlc.arg(node_public_id) AND n.kind = 'file' AND n.trashed_at IS NOT NULL
  AND l.owner_id = sqlc.arg(owner_id) AND l.encryption_format <> 'v2';

-- name: TrashFileNode :one
UPDATE nodes
SET trashed_at = now(), updated_at = now()
WHERE id = $1 AND kind = 'file' AND trashed_at IS NULL
RETURNING *;

-- name: SetFileTrashedAt :exec
UPDATE nodes SET trashed_at = $2, updated_at = now() WHERE id = $1 AND kind = 'file';

-- name: RestoreFileNode :one
UPDATE nodes
SET trashed_at = NULL, updated_at = now()
WHERE id = $1 AND kind = 'file' AND trashed_at IS NOT NULL
RETURNING *;

-- name: ListTrashedFilesByOwner :many
SELECT n.*, l.public_id AS library_public_id, l.name AS library_name,
       l.encryption_mode, parent.public_id AS parent_public_id
FROM nodes n
JOIN libraries l ON l.id = n.library_id
JOIN nodes parent ON parent.id = n.parent_id
WHERE l.owner_id = sqlc.arg(owner_id) AND l.encryption_format <> 'v2' AND n.kind = 'file' AND n.trashed_at IS NOT NULL
  AND n.id > sqlc.arg(after_id)
ORDER BY n.id
LIMIT sqlc.arg(page_limit);

-- name: GetTrashedFileCursorByPublicIDAndOwner :one
SELECT n.id
FROM nodes n
JOIN libraries l ON l.id = n.library_id
WHERE n.public_id = sqlc.arg(node_public_id) AND n.kind = 'file' AND n.trashed_at IS NOT NULL
  AND l.owner_id = sqlc.arg(owner_id) AND l.encryption_format <> 'v2';

-- name: ListExpiredTrashedFileIDs :many
SELECT id
FROM nodes
WHERE kind = 'file' AND trashed_at <= now() - INTERVAL '30 days'
ORDER BY trashed_at, id
LIMIT $1;

-- name: GetExpiredTrashedFileForUpdate :one
SELECT id, library_id
FROM nodes
WHERE id = $1 AND kind = 'file' AND trashed_at <= now() - INTERVAL '30 days'
FOR UPDATE;

-- name: GetTrashedFileForUpdate :one
SELECT id, library_id
FROM nodes
WHERE id = $1 AND kind = 'file' AND trashed_at IS NOT NULL
FOR UPDATE;

-- name: RenameFileNode :one
UPDATE nodes
SET name = $2, encrypted_name = $3, name_token = $4, updated_at = now()
WHERE id = $1 AND kind = 'file' AND trashed_at IS NULL
RETURNING *;

-- name: GetMoveDestination :one
SELECT l.id, l.public_id, l.name, l.backend_id, l.encryption_mode,
       root.id AS root_node_id, root.public_id AS root_node_public_id
FROM libraries l
JOIN nodes root ON root.id = l.root_node_id
WHERE l.public_id = sqlc.arg(library_public_id) AND l.owner_id = sqlc.arg(owner_id) AND l.encryption_format <> 'v2';

-- name: GetMoveFolder :one
SELECT n.id, n.public_id
FROM nodes n
JOIN libraries l ON l.id = n.library_id
WHERE n.public_id = sqlc.arg(node_public_id) AND l.owner_id = sqlc.arg(owner_id) AND l.encryption_format <> 'v2'
  AND n.library_id = sqlc.arg(library_id) AND n.kind = 'folder' AND n.trashed_at IS NULL;

-- name: MoveFileNode :one
WITH file_blobs AS (
    SELECT DISTINCT v.blob_id
    FROM file_versions v
    WHERE v.node_id = sqlc.arg(node_id)
), movable AS (
    SELECT (SELECT library_id FROM nodes WHERE id = sqlc.arg(node_id)) = sqlc.arg(destination_library_id)
      OR NOT EXISTS (
        SELECT 1
        FROM file_versions v
        JOIN file_blobs b ON b.blob_id = v.blob_id
        WHERE v.node_id <> sqlc.arg(node_id)
    ) AS ok
), removed_tags AS (
    DELETE FROM file_tags
    WHERE node_id = sqlc.arg(node_id)
      AND library_id <> sqlc.arg(destination_library_id)
), moved_uploads AS (
    UPDATE upload_sessions
    SET library_id = sqlc.arg(destination_library_id),
        parent_id = sqlc.arg(destination_parent_id), updated_at = now()
    WHERE (target_node_id = sqlc.arg(node_id) OR published_node_id = sqlc.arg(node_id))
      AND (SELECT ok FROM movable)
), moved_locations AS (
    UPDATE blob_locations
    SET library_id = sqlc.arg(destination_library_id), updated_at = now()
    WHERE blob_id IN (SELECT blob_id FROM file_blobs) AND (SELECT ok FROM movable)
), moved_versions AS (
    UPDATE file_versions
    SET library_id = sqlc.arg(destination_library_id)
    WHERE node_id = sqlc.arg(node_id) AND (SELECT ok FROM movable)
), moved_blobs AS (
    UPDATE blobs
    SET library_id = sqlc.arg(destination_library_id)
    WHERE id IN (SELECT blob_id FROM file_blobs) AND (SELECT ok FROM movable)
)
UPDATE nodes
SET library_id = sqlc.arg(destination_library_id), parent_id = sqlc.arg(destination_parent_id), updated_at = now()
WHERE nodes.id = sqlc.arg(node_id) AND nodes.kind = 'file' AND nodes.trashed_at IS NULL AND (SELECT ok FROM movable)
RETURNING nodes.*;

-- name: DetachUploadSessionsFromNode :exec
UPDATE upload_sessions
SET target_node_id = CASE WHEN target_node_id = sqlc.arg(node_id) THEN NULL ELSE target_node_id END,
    published_node_id = CASE WHEN published_node_id = sqlc.arg(node_id) THEN NULL ELSE published_node_id END,
    published_version_id = CASE WHEN published_node_id = sqlc.arg(node_id) THEN NULL ELSE published_version_id END,
    updated_at = now()
WHERE target_node_id = sqlc.arg(node_id) OR published_node_id = sqlc.arg(node_id);

-- name: DeleteFileVersions :many
DELETE FROM file_versions WHERE node_id = $1
RETURNING blob_id;

-- name: DeleteFileNode :execrows
DELETE FROM nodes WHERE id = $1 AND kind = 'file';

-- name: DeleteUnreferencedBlobLocations :many
WITH deleted AS (
    DELETE FROM blob_locations bl
    WHERE bl.blob_id = ANY(sqlc.arg(blob_ids)::bigint[])
      AND NOT EXISTS (SELECT 1 FROM file_versions v WHERE v.blob_id = bl.blob_id)
    RETURNING bl.backend_id, bl.object_key
)
SELECT sb.public_id AS backend_public_id, deleted.object_key
FROM deleted
JOIN storage_backends sb ON sb.id = deleted.backend_id;

-- name: DeleteUnreferencedBlobs :exec
DELETE FROM blobs b
WHERE b.id = ANY(sqlc.arg(blob_ids)::bigint[])
  AND NOT EXISTS (SELECT 1 FROM file_versions v WHERE v.blob_id = b.id);

-- name: ClearFileCurrentVersion :exec
UPDATE nodes SET current_version_id = NULL WHERE id = $1 AND kind = 'file';
