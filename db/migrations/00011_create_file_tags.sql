-- +goose Up
CREATE TABLE tags (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    public_id TEXT NOT NULL UNIQUE,
    library_id BIGINT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    name TEXT,
    encrypted_name BYTEA,
    name_token BYTEA CHECK (name_token IS NULL OR octet_length(name_token) = 32),
    color TEXT CHECK (color IS NULL OR color ~ '^#[0-9A-F]{6}$'),
    encrypted_color BYTEA,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT tags_name_check CHECK (
        (name IS NOT NULL AND encrypted_name IS NULL AND name_token IS NULL AND color IS NOT NULL AND encrypted_color IS NULL) OR
        (name IS NULL AND encrypted_name IS NOT NULL AND name_token IS NOT NULL AND color IS NULL AND encrypted_color IS NOT NULL)
    ),
    UNIQUE (id, library_id)
);

CREATE UNIQUE INDEX tags_plain_name_key ON tags (library_id, lower(name)) WHERE name IS NOT NULL;
CREATE UNIQUE INDEX tags_encrypted_name_key ON tags (library_id, name_token) WHERE name_token IS NOT NULL;

CREATE TABLE file_tags (
    node_id BIGINT NOT NULL,
    tag_id BIGINT NOT NULL,
    library_id BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (node_id, tag_id),
    FOREIGN KEY (node_id, library_id) REFERENCES nodes(id, library_id) ON DELETE CASCADE,
    FOREIGN KEY (tag_id, library_id) REFERENCES tags(id, library_id) ON DELETE CASCADE
);

CREATE INDEX file_tags_tag_id_idx ON file_tags (tag_id, node_id);

-- +goose Down
DROP TABLE file_tags;
DROP TABLE tags;
