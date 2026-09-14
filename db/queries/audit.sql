-- name: CreateAuditEvent :exec
INSERT INTO audit_events (
    public_id, action, actor_type, actor_id, subject_id, target_id, details, ip_address, user_agent, request_id
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10);

-- name: GetAuditEventCursor :one
SELECT id FROM audit_events WHERE public_id = $1;

-- name: ListAuditEvents :many
SELECT e.id, e.public_id, e.action, e.actor_type, e.actor_id, e.target_id, e.details,
       e.ip_address, e.user_agent, e.request_id, e.created_at,
       actor.public_id AS actor_public_id, actor.name AS actor_name, actor.email AS actor_email,
       subject.public_id AS subject_public_id, subject.name AS subject_name, subject.email AS subject_email
FROM audit_events e
LEFT JOIN users actor ON actor.id = e.actor_id
LEFT JOIN users subject ON subject.id = e.subject_id
WHERE (sqlc.arg(before_id)::bigint = 0 OR e.id < sqlc.arg(before_id)::bigint)
  AND (sqlc.arg(user_id)::bigint = 0 OR e.actor_id = sqlc.arg(user_id)::bigint OR e.subject_id = sqlc.arg(user_id)::bigint)
  AND (sqlc.arg(action)::text = '' OR e.action = sqlc.arg(action)::text)
  AND (sqlc.narg(from_time)::timestamptz IS NULL OR e.created_at >= sqlc.narg(from_time)::timestamptz)
  AND (sqlc.narg(to_time)::timestamptz IS NULL OR e.created_at < sqlc.narg(to_time)::timestamptz)
ORDER BY e.id DESC
LIMIT sqlc.arg(page_limit);

-- name: DeleteExpiredAuditEvents :execrows
DELETE FROM audit_events
WHERE id IN (
    SELECT id FROM audit_events
    WHERE created_at < sqlc.arg(cutoff)::timestamptz
    ORDER BY id
    LIMIT sqlc.arg(batch_size)
);

-- name: GetFileAuditTarget :one
SELECT n.public_id, n.name, l.public_id AS library_public_id, l.name AS library_name,
       l.encryption_mode, l.owner_id
FROM nodes n
JOIN libraries l ON l.id = n.library_id
WHERE n.id = $1;
