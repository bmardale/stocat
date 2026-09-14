-- name: CreateV2Library :exec
INSERT INTO libraries (
    id, public_id, owner_id, backend_id, root_node_id, encryption_mode, encryption_format,
    encrypted_root_metadata, owner_root_envelope
)
OVERRIDING SYSTEM VALUE
VALUES ($1, $2, $3, $4, $5, 'e2ee', 'v2', $6, $7);

-- name: CreateV2RootNode :exec
INSERT INTO nodes (id, public_id, library_id, kind, key_epoch, metadata_revision, metadata_signature)
OVERRIDING SYSTEM VALUE
VALUES ($1, $2, $3, 'folder', 1, 1, $4);

-- name: ListV2LibrariesByOwner :many
SELECT l.public_id, l.encrypted_root_metadata, l.owner_root_envelope, l.created_at, l.updated_at,
       b.public_id AS backend_public_id, b.name AS backend_name, b.type AS backend_type,
       root.public_id AS root_node_public_id, root.key_epoch AS root_key_epoch,
       root.metadata_revision AS root_metadata_revision, root.metadata_signature AS root_metadata_signature
FROM libraries l
JOIN storage_backends b ON b.id = l.backend_id
JOIN nodes root ON root.id = l.root_node_id
WHERE l.owner_id = $1 AND l.encryption_format = 'v2'
ORDER BY l.id;

-- name: GetV2LibraryByPublicIDAndOwner :one
SELECT l.id, l.public_id, l.encrypted_root_metadata, l.owner_root_envelope, l.created_at, l.updated_at,
       b.public_id AS backend_public_id, b.name AS backend_name, b.type AS backend_type,
       root.id AS root_node_id, root.public_id AS root_node_public_id, root.key_epoch AS root_key_epoch,
       root.metadata_revision AS root_metadata_revision, root.metadata_signature AS root_metadata_signature
FROM libraries l
JOIN storage_backends b ON b.id = l.backend_id
JOIN nodes root ON root.id = l.root_node_id
WHERE l.public_id = $1 AND l.owner_id = $2 AND l.encryption_format = 'v2';

-- name: LockV2Library :one
SELECT l.id, root.id AS root_node_id, root.public_id AS root_node_public_id,
       root.key_epoch AS root_key_epoch, root.metadata_revision AS root_metadata_revision
FROM libraries l
JOIN nodes root ON root.id = l.root_node_id
WHERE l.public_id = $1 AND l.owner_id = $2 AND l.encryption_format = 'v2'
FOR UPDATE OF l, root;

-- name: UpdateV2RootMetadata :exec
UPDATE libraries SET encrypted_root_metadata = $2, updated_at = now()
WHERE id = $1 AND encryption_format = 'v2';

-- name: UpdateV2RootMetadataSignature :exec
UPDATE nodes SET metadata_revision = $2, metadata_signature = $3, updated_at = now()
WHERE id = $1 AND parent_id IS NULL;

-- name: GetV2NodeByPublicIDAndOwner :one
SELECT n.*, l.public_id AS library_public_id
FROM nodes n
JOIN libraries l ON l.id = n.library_id
WHERE n.public_id = $1 AND l.owner_id = $2 AND l.encryption_format = 'v2';

-- name: LockV2NodeByPublicIDAndOwner :one
SELECT n.*, l.public_id AS library_public_id
FROM nodes n
JOIN libraries l ON l.id = n.library_id
WHERE n.public_id = $1 AND l.owner_id = $2 AND l.encryption_format = 'v2'
FOR UPDATE OF n;

-- name: CreateV2ChildNode :one
INSERT INTO nodes (
    public_id, library_id, parent_id, kind, name_token, encrypted_metadata, metadata_signature,
    key_epoch, metadata_revision
) VALUES ($1, $2, $3, $4, $5, $6, $7, 1, 1)
RETURNING id;

-- name: CreateNodeKeyEnvelope :exec
INSERT INTO node_key_envelopes (
    child_node_id, parent_node_id, library_id, child_epoch, parent_epoch, generation, ciphertext, owner_signature
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: UpdateV2NodeMetadata :exec
UPDATE nodes
SET encrypted_metadata = sqlc.arg(encrypted_metadata), metadata_signature = sqlc.arg(metadata_signature),
    metadata_revision = sqlc.arg(metadata_revision), name_token = sqlc.arg(name_token), updated_at = now()
WHERE id = sqlc.arg(id) AND parent_id IS NOT NULL AND trashed_at IS NULL;

-- name: GetV2NodeView :one
SELECT n.id, n.public_id, n.kind, n.name_token, n.encrypted_metadata, n.metadata_signature, n.key_epoch,
       n.metadata_revision, n.revision, n.current_version_pointer, n.current_version_signature,
       n.created_at, n.updated_at, parent.public_id AS parent_public_id,
       e.ciphertext AS parent_envelope, e.owner_signature AS parent_envelope_signature
FROM nodes n
JOIN nodes parent ON parent.id = n.parent_id
JOIN node_key_envelopes e ON e.child_node_id = n.id AND e.parent_node_id = n.parent_id
    AND e.child_epoch = n.key_epoch AND e.generation = n.visible_encryption_generation
WHERE n.id = $1;

-- name: ListV2ChildNodes :many
SELECT n.id, n.public_id, n.kind, n.name_token, n.encrypted_metadata, n.metadata_signature, n.key_epoch,
       n.metadata_revision, n.revision, n.current_version_pointer, n.current_version_signature,
       n.created_at, n.updated_at,
       e.ciphertext AS parent_envelope, e.owner_signature AS parent_envelope_signature
FROM nodes n
JOIN node_key_envelopes e ON e.child_node_id = n.id AND e.parent_node_id = n.parent_id
    AND e.child_epoch = n.key_epoch AND e.generation = n.visible_encryption_generation
WHERE n.library_id = sqlc.arg(library_id) AND n.parent_id = sqlc.arg(parent_id)
  AND n.trashed_at IS NULL AND n.id > sqlc.arg(after_id)
ORDER BY n.id
LIMIT sqlc.arg(page_limit);
