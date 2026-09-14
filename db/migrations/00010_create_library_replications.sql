-- +goose Up
CREATE TABLE library_replications (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    public_id TEXT NOT NULL UNIQUE,
    owner_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    source_library_id BIGINT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    destination_library_id BIGINT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    state TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'syncing', 'ready', 'failed')),
    last_error TEXT,
    last_synced_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (source_library_id <> destination_library_id),
    UNIQUE (source_library_id),
    UNIQUE (destination_library_id),
    UNIQUE (id, owner_id),
    FOREIGN KEY (source_library_id, owner_id) REFERENCES libraries(id, owner_id) ON DELETE CASCADE,
    FOREIGN KEY (destination_library_id, owner_id) REFERENCES libraries(id, owner_id) ON DELETE CASCADE
);

CREATE TABLE replication_nodes (
    replication_id BIGINT NOT NULL REFERENCES library_replications(id) ON DELETE CASCADE,
    source_node_id BIGINT NOT NULL,
    destination_node_id BIGINT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    PRIMARY KEY (replication_id, source_node_id),
    UNIQUE (replication_id, destination_node_id)
);

CREATE INDEX library_replications_owner_id_idx ON library_replications (owner_id, id);

-- +goose Down
DROP TABLE replication_nodes;
DROP TABLE library_replications;
