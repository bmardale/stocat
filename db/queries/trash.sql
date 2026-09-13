-- name: GetNodeByID :one
SELECT * FROM nodes WHERE id = $1;

-- name: GetLibraryRootNodeID :one
SELECT root_node_id FROM libraries WHERE id = $1;

-- name: TrashNodeSubtree :execrows
WITH RECURSIVE subtree AS (
    SELECT n.id FROM nodes n WHERE n.id = sqlc.arg(node_id)
    UNION ALL
    SELECT child.id FROM nodes child JOIN subtree parent ON child.parent_id = parent.id
)
UPDATE nodes SET trashed_at = COALESCE(nodes.trashed_at, now()), updated_at = now()
WHERE nodes.id IN (SELECT subtree.id FROM subtree);

-- name: RestoreNodeSubtree :execrows
WITH RECURSIVE subtree AS (
    SELECT n.id FROM nodes n WHERE n.id = sqlc.arg(node_id)
    UNION ALL
    SELECT child.id FROM nodes child JOIN subtree parent ON child.parent_id = parent.id
)
UPDATE nodes SET trashed_at = NULL, updated_at = now()
WHERE nodes.id IN (SELECT subtree.id FROM subtree) AND nodes.trashed_at IS NOT NULL;

-- name: ListTrashRootsByOwner :many
SELECT n.*, l.public_id AS library_public_id, l.name AS library_name,
       l.encryption_mode AS library_encryption_mode, parent.public_id AS parent_public_id
FROM nodes n
JOIN libraries l ON l.id = n.library_id
JOIN nodes parent ON parent.id = n.parent_id
WHERE l.owner_id = sqlc.arg(owner_id)
  AND n.trashed_at IS NOT NULL AND parent.trashed_at IS NULL
  AND n.id > sqlc.arg(after_id)
ORDER BY n.id
LIMIT sqlc.arg(page_limit);

-- name: ListExpiredTrashRoots :many
SELECT n.id, n.public_id
FROM nodes n
JOIN nodes parent ON parent.id = n.parent_id
WHERE n.trashed_at IS NOT NULL AND n.trashed_at < sqlc.arg(cutoff)
  AND parent.trashed_at IS NULL
ORDER BY n.id
LIMIT sqlc.arg(page_limit);

-- name: CountActiveUploadSessionsInSubtree :one
WITH RECURSIVE subtree AS (
    SELECT n.id FROM nodes n WHERE n.id = sqlc.arg(node_id)
    UNION ALL
    SELECT child.id FROM nodes child JOIN subtree parent ON child.parent_id = parent.id
)
SELECT count(*)::bigint FROM upload_sessions
WHERE upload_sessions.parent_id IN (SELECT subtree.id FROM subtree)
  AND upload_sessions.state IN ('created', 'uploading', 'uploaded', 'finalizing');

-- name: PrepareUploadSessionsForSubtreeRemoval :exec
WITH RECURSIVE subtree AS (
    SELECT n.id FROM nodes n WHERE n.id = sqlc.arg(node_id)
    UNION ALL
    SELECT child.id FROM nodes child JOIN subtree parent ON child.parent_id = parent.id
)
UPDATE upload_sessions
SET parent_id = CASE WHEN upload_sessions.parent_id IN (SELECT subtree.id FROM subtree) THEN sqlc.arg(root_node_id) ELSE upload_sessions.parent_id END,
    target_node_id = CASE WHEN upload_sessions.target_node_id IN (SELECT subtree.id FROM subtree) THEN NULL ELSE upload_sessions.target_node_id END,
    published_node_id = CASE WHEN upload_sessions.published_node_id IN (SELECT subtree.id FROM subtree) THEN NULL ELSE upload_sessions.published_node_id END,
    published_version_id = CASE WHEN upload_sessions.published_node_id IN (SELECT subtree.id FROM subtree) THEN NULL ELSE upload_sessions.published_version_id END,
    updated_at = now()
WHERE upload_sessions.parent_id IN (SELECT subtree.id FROM subtree)
   OR upload_sessions.target_node_id IN (SELECT subtree.id FROM subtree)
   OR upload_sessions.published_node_id IN (SELECT subtree.id FROM subtree);

-- name: ClearSubtreeCurrentVersion :exec
WITH RECURSIVE subtree AS (
    SELECT n.id FROM nodes n WHERE n.id = sqlc.arg(node_id)
    UNION ALL
    SELECT child.id FROM nodes child JOIN subtree parent ON child.parent_id = parent.id
)
UPDATE nodes SET current_version_id = NULL WHERE nodes.id IN (SELECT subtree.id FROM subtree);

-- name: DeleteSubtreeFileVersions :many
WITH RECURSIVE subtree AS (
    SELECT n.id FROM nodes n WHERE n.id = sqlc.arg(node_id)
    UNION ALL
    SELECT child.id FROM nodes child JOIN subtree parent ON child.parent_id = parent.id
)
DELETE FROM file_versions WHERE file_versions.node_id IN (SELECT subtree.id FROM subtree)
RETURNING blob_id;

-- name: DeleteSubtreeNodes :execrows
WITH RECURSIVE subtree AS (
    SELECT n.id FROM nodes n WHERE n.id = sqlc.arg(node_id)
    UNION ALL
    SELECT child.id FROM nodes child JOIN subtree parent ON child.parent_id = parent.id
)
DELETE FROM nodes WHERE nodes.id IN (SELECT subtree.id FROM subtree);
