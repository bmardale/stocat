-- name: FindBlobByDedupFingerprint :one
SELECT * FROM blobs
WHERE library_id = $1 AND dedup_fingerprint = $2;

-- name: CreateBlob :one
INSERT INTO blobs (
    public_id, library_id, size_bytes, ciphertext_sha256, dedup_fingerprint, encryption_format,
    encrypted_file_key
) VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: CreateBlobLocation :one
INSERT INTO blob_locations (public_id, blob_id, library_id, backend_id, object_key, state)
VALUES ($1, $2, $3, $4, $5, 'pending')
RETURNING *;

-- name: MarkBlobLocationAvailable :one
UPDATE blob_locations
SET state = 'available', verified_at = now(), updated_at = now()
WHERE id = $1 AND state = 'pending'
RETURNING *;

-- name: GetAvailableBlobLocation :one
SELECT * FROM blob_locations
WHERE blob_id = $1 AND state = 'available';

-- name: CountBlobVersionReferences :one
SELECT count(*) FROM file_versions WHERE blob_id = $1;

-- name: CreateFileVersion :one
INSERT INTO file_versions (
    public_id, node_id, library_id, ordinal, blob_id, size_bytes, content_sha256,
    restored_from_version_id
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;
