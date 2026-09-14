-- +goose Up
CREATE TABLE audit_events (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    public_id TEXT NOT NULL UNIQUE,
    action TEXT NOT NULL,
    actor_type TEXT NOT NULL CHECK (actor_type IN ('user', 'system')),
    actor_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
    subject_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
    target_id TEXT NOT NULL DEFAULT '',
    details JSONB NOT NULL DEFAULT '{}',
    ip_address INET,
    user_agent TEXT NOT NULL DEFAULT '',
    request_id TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT audit_events_actor_check CHECK (actor_type = 'user' OR actor_id IS NULL)
);

CREATE INDEX audit_events_actor_id_idx ON audit_events (actor_id, id);
CREATE INDEX audit_events_subject_id_idx ON audit_events (subject_id, id);
CREATE INDEX audit_events_created_at_idx ON audit_events (created_at);

-- +goose Down
DROP TABLE audit_events;
