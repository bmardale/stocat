-- name: GetV2CurrentFileByPublicIDAndOwner :one
SELECT
    n.public_id AS node_public_id,
    n.revision,
    n.current_version_pointer,
    n.current_version_signature,
    l.public_id AS library_public_id,
    v.public_id AS version_public_id,
    v.size_bytes,
    b.size_bytes AS stored_size_bytes,
    b.ciphertext_sha256,
    k.encrypted_content_key,
    k.signed_manifest,
    k.manifest_signature,
    sb.public_id AS backend_public_id,
    bl.object_key
FROM nodes n
JOIN libraries l ON l.id = n.library_id
JOIN file_versions v ON v.id = n.current_version_id
JOIN file_version_keys k ON k.version_id = v.id
JOIN blobs b ON b.id = v.blob_id
JOIN blob_locations bl ON bl.blob_id = b.id AND bl.backend_id = l.backend_id AND bl.state = 'available'
JOIN storage_backends sb ON sb.id = bl.backend_id
WHERE n.public_id = sqlc.arg(node_public_id) AND n.kind = 'file' AND n.trashed_at IS NULL
  AND l.owner_id = sqlc.arg(owner_id) AND l.encryption_format = 'v2';

-- name: GetV2FileNodeByPublicIDAndOwner :one
SELECT n.id, n.public_id, n.library_id, n.key_epoch, n.visible_encryption_generation, n.trashed_at,
       l.public_id AS library_public_id
FROM nodes n
JOIN libraries l ON l.id = n.library_id
WHERE n.public_id = sqlc.arg(node_public_id) AND n.kind = 'file'
  AND l.owner_id = sqlc.arg(owner_id) AND l.encryption_format = 'v2';

-- name: LockV2NodeEnvelope :one
SELECT * FROM node_key_envelopes
WHERE child_node_id = $1 AND child_epoch = $2 AND generation = $3
FOR UPDATE;

-- name: MoveV2FileNode :execrows
UPDATE nodes
SET parent_id = sqlc.arg(parent_id), name_token = sqlc.arg(name_token), updated_at = now()
WHERE id = sqlc.arg(id) AND kind = 'file' AND trashed_at IS NULL;

-- name: UpdateNodeKeyEnvelopeParent :exec
UPDATE node_key_envelopes
SET parent_node_id = sqlc.arg(parent_node_id), parent_epoch = sqlc.arg(parent_epoch),
    ciphertext = sqlc.arg(ciphertext), owner_signature = sqlc.arg(owner_signature)
WHERE child_node_id = sqlc.arg(child_node_id) AND child_epoch = sqlc.arg(child_epoch)
  AND generation = sqlc.arg(generation);

-- name: ListV2TrashedFilesByOwner :many
SELECT n.id, n.public_id, n.name_token, n.encrypted_metadata, n.metadata_signature, n.key_epoch,
       n.metadata_revision, n.revision, n.trashed_at,
       l.public_id AS library_public_id, parent.public_id AS parent_public_id,
       e.ciphertext AS parent_envelope, e.owner_signature AS parent_envelope_signature
FROM nodes n
JOIN libraries l ON l.id = n.library_id
JOIN nodes parent ON parent.id = n.parent_id
JOIN node_key_envelopes e ON e.child_node_id = n.id AND e.parent_node_id = n.parent_id
    AND e.child_epoch = n.key_epoch AND e.generation = n.visible_encryption_generation
WHERE l.owner_id = sqlc.arg(owner_id) AND l.encryption_format = 'v2' AND n.kind = 'file'
  AND n.trashed_at IS NOT NULL AND n.id > sqlc.arg(after_id)
ORDER BY n.id
LIMIT sqlc.arg(page_limit);
