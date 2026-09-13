-- name: CreateUploadSession :one
INSERT INTO upload_sessions (
    public_id, owner_id, library_id, parent_id, target_node_id, expected_revision,
    name, encrypted_name, name_token, declared_size, staging_key, destination_key,
    state, expires_at
) VALUES (
    $1, $2, $3, $4, $5, $6,
    $7, $8, $9, $10, $11, $12,
    'created', $13
)
RETURNING *;

-- name: GetUploadSessionByPublicIDAndOwner :one
SELECT u.*
FROM upload_sessions u
WHERE u.public_id = $1 AND u.owner_id = $2;

-- name: GetPublishedUploadIDs :one
SELECT n.public_id AS node_public_id, v.public_id AS version_public_id
FROM upload_sessions u
JOIN nodes n ON n.id = u.published_node_id
JOIN file_versions v ON v.id = u.published_version_id
WHERE u.id = $1;

-- name: GetFileVersionByID :one
SELECT * FROM file_versions WHERE id = $1;

-- name: GetUploadSessionForUpdate :one
SELECT * FROM upload_sessions WHERE id = $1 FOR UPDATE;

-- name: LockLibraryForUpload :one
SELECT l.*, u.default_quota_mb, b.public_id AS backend_public_id
FROM libraries l
JOIN users u ON u.id = l.owner_id
JOIN storage_backends b ON b.id = l.backend_id
WHERE l.public_id = $1 AND l.owner_id = $2 AND b.enabled = true
FOR UPDATE OF l;

-- name: LockUploadAdmission :exec
SELECT pg_advisory_xact_lock(1937011297);

-- name: LockLibraryByID :exec
SELECT id FROM libraries WHERE id = $1 FOR UPDATE;

-- name: LockNodeForPublication :one
SELECT * FROM nodes WHERE id = $1 FOR UPDATE;

-- name: GetUploadPublication :one
SELECT u.*, l.public_id AS library_public_id, l.backend_id, l.encryption_mode,
       b.public_id AS backend_public_id
FROM upload_sessions u
JOIN libraries l ON l.id = u.library_id
JOIN storage_backends b ON b.id = l.backend_id
WHERE u.id = $1;

-- name: AdvanceUploadOffset :one
UPDATE upload_sessions
SET upload_offset = sqlc.arg(next_offset),
    state = CASE WHEN sqlc.arg(next_offset) = declared_size THEN 'uploaded' ELSE 'uploading' END,
    updated_at = now()
WHERE id = sqlc.arg(id) AND upload_offset = sqlc.arg(current_offset)
  AND state IN ('created', 'uploading')
RETURNING *;

-- name: SetUploadFinalizing :one
UPDATE upload_sessions
SET dedup_fingerprint = sqlc.arg(dedup_fingerprint),
    encryption_format = sqlc.arg(encryption_format),
    encrypted_file_key = sqlc.arg(encrypted_file_key),
    state = 'finalizing', failure_code = NULL, failure_message = NULL, updated_at = now()
WHERE id = sqlc.arg(id) AND state = 'uploaded' AND upload_offset = declared_size
RETURNING *;

-- name: SetPlainUploadFinalizing :one
UPDATE upload_sessions
SET state = 'finalizing', failure_code = NULL, failure_message = NULL, updated_at = now()
WHERE id = sqlc.arg(id) AND state = 'uploaded' AND upload_offset = declared_size
RETURNING *;

-- name: SetZeroLengthUploadReady :exec
UPDATE upload_sessions SET state = 'uploaded', updated_at = now()
WHERE id = $1 AND declared_size = 0 AND state = 'created';

-- name: SetUploadFingerprint :exec
UPDATE upload_sessions SET dedup_fingerprint = $2, updated_at = now() WHERE id = $1;

-- name: CancelUploadSession :one
UPDATE upload_sessions
SET state = 'cancelled', updated_at = now()
WHERE public_id = $1 AND owner_id = $2
  AND state IN ('created', 'uploading', 'uploaded', 'failed')
RETURNING *;

-- name: FailUploadSession :exec
UPDATE upload_sessions
SET state = $2, failure_code = $3, failure_message = $4, updated_at = now()
WHERE id = $1 AND state = 'finalizing';

-- name: MarkUploadFailed :exec
UPDATE upload_sessions
SET state = 'failed', failure_code = $2, failure_message = $3, updated_at = now()
WHERE id = $1 AND state IN ('created', 'uploading', 'uploaded', 'finalizing');

-- name: ExpireUploadSessions :many
UPDATE upload_sessions
SET state = 'expired', updated_at = now()
WHERE expires_at < now() AND state IN ('created', 'uploading', 'uploaded', 'failed')
RETURNING staging_key;

-- name: CompleteUploadSession :exec
UPDATE upload_sessions
SET state = 'completed', published_node_id = $2, published_version_id = $3,
    completed_at = now(), updated_at = now(), failure_code = NULL, failure_message = NULL
WHERE id = $1 AND state = 'finalizing';

-- name: SumLibraryUploadReservations :one
SELECT coalesce(sum(declared_size), 0)::bigint
FROM upload_sessions
WHERE library_id = sqlc.arg(library_id)
  AND state IN ('created', 'uploading', 'uploaded', 'finalizing', 'failed');

-- name: SumAllUploadReservations :one
SELECT coalesce(sum(declared_size), 0)::bigint
FROM upload_sessions
WHERE state IN ('created', 'uploading', 'uploaded', 'finalizing', 'failed');

-- name: SumLibraryStoredBytes :one
SELECT coalesce(sum(size_bytes), 0)::bigint
FROM blobs
WHERE library_id = $1;

-- name: NextFileVersionOrdinal :one
SELECT coalesce(max(ordinal), 0)::bigint + 1
FROM file_versions WHERE node_id = $1;

-- name: UpdateNodeCurrentVersion :exec
UPDATE nodes
SET current_version_id = $2, revision = revision + 1, updated_at = now()
WHERE id = $1;

-- name: CreateUploadFileNode :one
INSERT INTO nodes (public_id, library_id, parent_id, kind, name, encrypted_name, name_token)
VALUES ($1, $2, $3, 'file', $4, $5, $6)
RETURNING *;
