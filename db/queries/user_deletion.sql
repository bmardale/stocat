-- name: LockUserUploadSessions :many
SELECT id FROM upload_sessions WHERE owner_id = $1 ORDER BY id FOR UPDATE;

-- name: LockUserLibraries :many
SELECT id FROM libraries WHERE owner_id = $1 ORDER BY id FOR UPDATE;

-- name: ListUserBlobDeletionTargets :many
SELECT sb.public_id AS backend_public_id, bl.object_key
FROM blob_locations bl
JOIN libraries l ON l.id = bl.library_id
JOIN storage_backends sb ON sb.id = bl.backend_id
WHERE l.owner_id = $1;

-- name: ListUserUploadDeletionTargets :many
SELECT sb.public_id AS backend_public_id, u.destination_key, u.staging_key
FROM upload_sessions u
JOIN libraries l ON l.id = u.library_id
JOIN storage_backends sb ON sb.id = l.backend_id
WHERE u.owner_id = $1;

-- name: DeleteUserUploadSessions :exec
DELETE FROM upload_sessions WHERE owner_id = $1;

-- name: ClearUserCurrentVersions :exec
UPDATE nodes
SET current_version_id = NULL
WHERE library_id IN (SELECT id FROM libraries WHERE owner_id = $1);

-- name: DeleteUserFileVersions :exec
DELETE FROM file_versions
WHERE library_id IN (SELECT id FROM libraries WHERE owner_id = $1);

-- name: DeleteUserBlobLocations :exec
DELETE FROM blob_locations
WHERE library_id IN (SELECT id FROM libraries WHERE owner_id = $1);

-- name: DeleteUserBlobs :exec
DELETE FROM blobs
WHERE library_id IN (SELECT id FROM libraries WHERE owner_id = $1);

-- name: DeleteUserLeafNodes :many
WITH leaves AS (
    SELECT n.id
    FROM nodes n
    JOIN libraries l ON l.id = n.library_id
    WHERE l.owner_id = $1
      AND n.id <> l.root_node_id
      AND NOT EXISTS (SELECT 1 FROM nodes child WHERE child.parent_id = n.id)
)
DELETE FROM nodes
WHERE id IN (SELECT id FROM leaves)
RETURNING id;

-- name: DeleteUserLibraries :execrows
DELETE FROM libraries WHERE owner_id = $1;

-- name: DeleteUser :execrows
DELETE FROM users WHERE id = $1;
