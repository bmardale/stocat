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
