-- +goose Up
-- A file deletion clears the target of a replacement upload. The expected revision stays, so publication reports a conflict.
ALTER TABLE upload_sessions DROP CONSTRAINT upload_sessions_target_check;
ALTER TABLE upload_sessions ADD CONSTRAINT upload_sessions_target_check
    CHECK (target_node_id IS NULL OR expected_revision IS NOT NULL);

-- +goose Down
UPDATE upload_sessions
SET expected_revision = NULL,
    state = CASE WHEN state IN ('completed', 'conflict', 'cancelled', 'expired') THEN state ELSE 'conflict' END,
    updated_at = now()
WHERE target_node_id IS NULL AND expected_revision IS NOT NULL;
ALTER TABLE upload_sessions DROP CONSTRAINT upload_sessions_target_check;
ALTER TABLE upload_sessions ADD CONSTRAINT upload_sessions_target_check CHECK (
    (target_node_id IS NULL AND expected_revision IS NULL) OR
    (target_node_id IS NOT NULL AND expected_revision IS NOT NULL)
);
