-- name: LockV2LibraryForUpload :one
SELECT l.id, l.public_id, l.backend_id, b.name AS backend_name
FROM libraries l
JOIN storage_backends b ON b.id = l.backend_id
WHERE l.public_id = $1 AND l.owner_id = $2 AND l.encryption_format = 'v2' AND b.enabled = true
FOR UPDATE OF l;

-- name: CreateV2UploadSession :one
INSERT INTO upload_sessions (
    public_id, owner_id, library_id, parent_id, target_node_id, expected_revision, name_token,
    node_public_id, encrypted_metadata, metadata_signature, parent_envelope, parent_envelope_signature,
    declared_size, staging_key, destination_key, state, encryption_format, expires_at
) VALUES (
    sqlc.arg(public_id), sqlc.arg(owner_id), sqlc.arg(library_id), sqlc.arg(parent_id),
    sqlc.narg(target_node_id), sqlc.narg(expected_revision), sqlc.narg(name_token),
    sqlc.narg(node_public_id), sqlc.narg(encrypted_metadata), sqlc.narg(metadata_signature),
    sqlc.narg(parent_envelope), sqlc.narg(parent_envelope_signature),
    sqlc.arg(declared_size), sqlc.arg(staging_key), sqlc.arg(destination_key), 'created', 'stocat-framed-v2',
    sqlc.arg(expires_at)
)
RETURNING *;

-- name: SetV2UploadFinalizing :one
UPDATE upload_sessions
SET file_key_record = sqlc.arg(file_key_record), manifest_record = sqlc.arg(manifest_record),
    manifest_signature = sqlc.arg(manifest_signature), current_version_record = sqlc.arg(current_version_record),
    current_version_signature = sqlc.arg(current_version_signature),
    state = 'finalizing', failure_code = NULL, failure_message = NULL, updated_at = now()
WHERE id = sqlc.arg(id) AND state = 'uploaded' AND upload_offset = declared_size
  AND encryption_format = 'stocat-framed-v2'
RETURNING *;

-- name: CreateV2UploadFileNode :one
INSERT INTO nodes (
    public_id, library_id, parent_id, kind, name_token, encrypted_metadata, metadata_signature,
    key_epoch, metadata_revision
) VALUES ($1, $2, $3, 'file', $4, $5, $6, 1, 1)
RETURNING *;

-- name: CreateFileVersionKey :exec
INSERT INTO file_version_keys (
    version_id, node_id, node_epoch, generation, content_id, encrypted_content_key, signed_manifest,
    manifest_signature
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: SetV2CurrentVersion :one
UPDATE nodes
SET current_version_id = sqlc.arg(current_version_id), current_version_pointer = sqlc.arg(current_version_pointer),
    current_version_signature = sqlc.arg(current_version_signature), revision = revision + 1, updated_at = now()
WHERE id = sqlc.arg(id) AND kind = 'file'
RETURNING revision;

-- name: GetNodeByID :one
SELECT * FROM nodes WHERE id = $1;
