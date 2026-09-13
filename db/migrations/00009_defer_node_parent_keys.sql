-- +goose Up
-- Defer the parent checks. A trash purge deletes a whole folder tree in one statement.
ALTER TABLE nodes DROP CONSTRAINT nodes_parent_id_fkey;
ALTER TABLE nodes ADD CONSTRAINT nodes_parent_id_fkey
    FOREIGN KEY (parent_id) REFERENCES nodes(id) DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE nodes DROP CONSTRAINT nodes_parent_library_fkey;
ALTER TABLE nodes ADD CONSTRAINT nodes_parent_library_fkey
    FOREIGN KEY (parent_id, library_id) REFERENCES nodes(id, library_id) DEFERRABLE INITIALLY DEFERRED;

-- +goose Down
ALTER TABLE nodes DROP CONSTRAINT nodes_parent_id_fkey;
ALTER TABLE nodes ADD CONSTRAINT nodes_parent_id_fkey
    FOREIGN KEY (parent_id) REFERENCES nodes(id) ON DELETE RESTRICT;

ALTER TABLE nodes DROP CONSTRAINT nodes_parent_library_fkey;
ALTER TABLE nodes ADD CONSTRAINT nodes_parent_library_fkey
    FOREIGN KEY (parent_id, library_id) REFERENCES nodes(id, library_id);
