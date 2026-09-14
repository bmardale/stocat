-- name: ListLibraryReplicationsByOwner :many
SELECT r.*, source.public_id AS source_public_id, source.name AS source_name,
       destination.public_id AS destination_public_id, destination.name AS destination_name
FROM library_replications r
JOIN libraries source ON source.id = r.source_library_id
JOIN libraries destination ON destination.id = r.destination_library_id
WHERE r.owner_id = $1
ORDER BY source.name, r.id;

-- name: GetReplicationByPublicIDAndOwner :one
SELECT r.*, source.public_id AS source_public_id, source.name AS source_name,
       destination.public_id AS destination_public_id, destination.name AS destination_name,
       source.encryption_mode AS source_encryption_mode,
       destination.encryption_mode AS destination_encryption_mode,
       source.backend_id AS source_backend_id,
       destination.backend_id AS destination_backend_id
FROM library_replications r
JOIN libraries source ON source.id = r.source_library_id
JOIN libraries destination ON destination.id = r.destination_library_id
WHERE r.public_id = $1 AND r.owner_id = $2;

-- name: GetReplicationByID :one
SELECT r.*, source.public_id AS source_public_id, source.root_node_id AS source_root_node_id,
       destination.public_id AS destination_public_id, destination.root_node_id AS destination_root_node_id,
       source.backend_id AS source_backend_id, source_backend.public_id AS source_backend_public_id,
       destination.backend_id AS destination_backend_id, destination_backend.public_id AS destination_backend_public_id
FROM library_replications r
JOIN libraries source ON source.id = r.source_library_id
JOIN libraries destination ON destination.id = r.destination_library_id
JOIN storage_backends source_backend ON source_backend.id = source.backend_id
JOIN storage_backends destination_backend ON destination_backend.id = destination.backend_id
WHERE r.id = $1;

-- name: CreateLibraryReplication :one
WITH selected AS (
    SELECT source.id AS source_id, destination.id AS destination_id, source.key_envelope
    FROM libraries source, libraries destination
    WHERE source.public_id = sqlc.arg(source_public_id)
      AND destination.public_id = sqlc.arg(destination_public_id)
      AND source.owner_id = sqlc.arg(owner_id)
      AND destination.owner_id = sqlc.arg(owner_id)
      AND source.encryption_mode = destination.encryption_mode
      AND source.encryption_format <> 'v2' AND destination.encryption_format <> 'v2'
      AND NOT EXISTS (SELECT 1 FROM nodes n WHERE n.library_id = destination.id AND n.parent_id IS NOT NULL)
      AND NOT EXISTS (
          SELECT 1 FROM upload_sessions upload
          WHERE upload.library_id = destination.id
            AND upload.state IN ('created', 'uploading', 'uploaded', 'finalizing', 'failed')
      )
), prepared AS (
    UPDATE libraries destination
    SET key_envelope = selected.key_envelope, updated_at = now()
    FROM selected
    WHERE destination.id = selected.destination_id
    RETURNING selected.source_id, selected.destination_id
)
INSERT INTO library_replications (public_id, owner_id, source_library_id, destination_library_id)
SELECT sqlc.arg(public_id), sqlc.arg(owner_id), source_id, destination_id
FROM prepared
RETURNING *;

-- name: DeleteLibraryReplication :execrows
DELETE FROM library_replications WHERE public_id = $1 AND owner_id = $2;

-- name: LockLibraryReplication :one
SELECT * FROM library_replications WHERE id = $1 FOR UPDATE;

-- name: MarkReplicationSyncing :exec
UPDATE library_replications SET state = 'syncing', last_error = NULL, updated_at = now() WHERE id = $1;

-- name: MarkReplicationReady :exec
UPDATE library_replications SET state = 'ready', last_error = NULL, last_synced_at = now(), updated_at = now() WHERE id = $1;

-- name: MarkReplicationFailed :exec
UPDATE library_replications SET state = 'failed', last_error = $2, updated_at = now() WHERE id = $1;

-- name: ListReplicationIDs :many
SELECT id FROM library_replications ORDER BY id;

-- name: IsLibraryReplicationDestination :one
SELECT EXISTS (
    SELECT 1 FROM library_replications r
    JOIN libraries l ON l.id = r.destination_library_id
    WHERE l.public_id = $1 AND r.owner_id = $2
);

-- name: IsNodeInReplicationDestination :one
SELECT EXISTS (
    SELECT 1 FROM nodes n
    JOIN library_replications r ON r.destination_library_id = n.library_id
    WHERE n.public_id = $1 AND r.owner_id = $2
);

-- name: ListReplicationSourceNodes :many
SELECT n.*, parent_map.destination_node_id AS destination_parent_id,
       mapped.destination_node_id, current_version.public_id AS version_public_id,
       current_version.size_bytes, blob.id AS blob_id, blob.public_id AS blob_public_id,
       blob.size_bytes AS stored_size_bytes, blob.ciphertext_sha256, blob.dedup_fingerprint,
       blob.encryption_format, blob.encrypted_file_key, location.object_key
       , destination_blob.dedup_fingerprint AS destination_dedup_fingerprint
FROM nodes n
LEFT JOIN replication_nodes mapped ON mapped.replication_id = sqlc.arg(replication_id) AND mapped.source_node_id = n.id
LEFT JOIN replication_nodes parent_map ON parent_map.replication_id = sqlc.arg(replication_id) AND parent_map.source_node_id = n.parent_id
LEFT JOIN nodes destination_node ON destination_node.id = mapped.destination_node_id
LEFT JOIN file_versions destination_version ON destination_version.id = destination_node.current_version_id
LEFT JOIN blobs destination_blob ON destination_blob.id = destination_version.blob_id
LEFT JOIN file_versions current_version ON current_version.id = n.current_version_id
LEFT JOIN blobs blob ON blob.id = current_version.blob_id
LEFT JOIN blob_locations location ON location.blob_id = blob.id AND location.state = 'available'
WHERE n.library_id = sqlc.arg(source_library_id) AND n.parent_id IS NOT NULL
ORDER BY n.id;

-- name: CreateReplicatedNode :one
INSERT INTO nodes (public_id, library_id, parent_id, kind, name, encrypted_name, name_token, revision, trashed_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: UpdateReplicatedNode :exec
UPDATE nodes SET parent_id = $2, name = $3, encrypted_name = $4, name_token = $5,
    revision = $6, trashed_at = $7, updated_at = now()
WHERE id = $1;

-- name: MapReplicationNode :exec
INSERT INTO replication_nodes (replication_id, source_node_id, destination_node_id)
VALUES ($1, $2, $3);

-- name: GetDestinationBlobByFingerprint :one
SELECT * FROM blobs WHERE library_id = $1 AND dedup_fingerprint = $2;

-- name: CreateReplicatedBlob :one
INSERT INTO blobs (public_id, library_id, size_bytes, ciphertext_sha256, dedup_fingerprint, encryption_format, encrypted_file_key)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: CreateReplicatedBlobLocation :one
INSERT INTO blob_locations (public_id, blob_id, library_id, backend_id, object_key, state, verified_at)
VALUES ($1, $2, $3, $4, $5, 'available', now())
RETURNING *;

-- name: CreateReplicatedFileVersion :one
INSERT INTO file_versions (public_id, node_id, library_id, ordinal, blob_id, size_bytes)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: SetReplicatedNodeVersion :exec
UPDATE nodes SET current_version_id = $2, updated_at = now() WHERE id = $1;

-- name: GetReplicationDestinationVersion :one
SELECT source_version.public_id AS source_version_public_id
FROM replication_nodes mapping
JOIN nodes destination_node ON destination_node.id = mapping.destination_node_id
JOIN file_versions destination_version ON destination_version.id = destination_node.current_version_id
JOIN file_versions source_version ON source_version.node_id = mapping.source_node_id
WHERE mapping.replication_id = $1 AND mapping.source_node_id = $2
  AND source_version.ordinal = destination_version.ordinal;

-- name: ListRemovedReplicationNodes :many
SELECT mapping.destination_node_id
FROM replication_nodes mapping
LEFT JOIN nodes source ON source.id = mapping.source_node_id
WHERE mapping.replication_id = $1 AND (source.id IS NULL OR source.library_id <> $2);

-- name: DeleteReplicationNodeMapping :exec
DELETE FROM replication_nodes WHERE replication_id = $1 AND destination_node_id = $2;

-- name: TrashReplicatedNode :exec
UPDATE nodes SET trashed_at = now(), updated_at = now() WHERE id = $1;
