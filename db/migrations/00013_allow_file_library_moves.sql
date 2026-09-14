-- +goose Up
ALTER TABLE nodes DROP CONSTRAINT nodes_parent_library_fkey;
ALTER TABLE nodes ADD CONSTRAINT nodes_parent_library_fkey
    FOREIGN KEY (parent_id, library_id) REFERENCES nodes(id, library_id)
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE file_versions DROP CONSTRAINT file_versions_node_id_library_id_fkey;
ALTER TABLE file_versions ADD CONSTRAINT file_versions_node_id_library_id_fkey
    FOREIGN KEY (node_id, library_id) REFERENCES nodes(id, library_id) ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE file_versions DROP CONSTRAINT file_versions_blob_id_library_id_fkey;
ALTER TABLE file_versions ADD CONSTRAINT file_versions_blob_id_library_id_fkey
    FOREIGN KEY (blob_id, library_id) REFERENCES blobs(id, library_id) ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE blob_locations DROP CONSTRAINT blob_locations_blob_id_library_id_fkey;
ALTER TABLE blob_locations ADD CONSTRAINT blob_locations_blob_id_library_id_fkey
    FOREIGN KEY (blob_id, library_id) REFERENCES blobs(id, library_id) ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE upload_sessions DROP CONSTRAINT upload_sessions_parent_id_library_id_fkey;
ALTER TABLE upload_sessions ADD CONSTRAINT upload_sessions_parent_id_library_id_fkey
    FOREIGN KEY (parent_id, library_id) REFERENCES nodes(id, library_id) ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE upload_sessions DROP CONSTRAINT upload_sessions_target_node_id_library_id_fkey;
ALTER TABLE upload_sessions ADD CONSTRAINT upload_sessions_target_node_id_library_id_fkey
    FOREIGN KEY (target_node_id, library_id) REFERENCES nodes(id, library_id) ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE upload_sessions DROP CONSTRAINT upload_sessions_published_node_id_library_id_fkey;
ALTER TABLE upload_sessions ADD CONSTRAINT upload_sessions_published_node_id_library_id_fkey
    FOREIGN KEY (published_node_id, library_id) REFERENCES nodes(id, library_id) ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

-- +goose Down
ALTER TABLE upload_sessions DROP CONSTRAINT upload_sessions_published_node_id_library_id_fkey;
ALTER TABLE upload_sessions ADD CONSTRAINT upload_sessions_published_node_id_library_id_fkey
    FOREIGN KEY (published_node_id, library_id) REFERENCES nodes(id, library_id) ON DELETE RESTRICT;

ALTER TABLE upload_sessions DROP CONSTRAINT upload_sessions_target_node_id_library_id_fkey;
ALTER TABLE upload_sessions ADD CONSTRAINT upload_sessions_target_node_id_library_id_fkey
    FOREIGN KEY (target_node_id, library_id) REFERENCES nodes(id, library_id) ON DELETE RESTRICT;

ALTER TABLE upload_sessions DROP CONSTRAINT upload_sessions_parent_id_library_id_fkey;
ALTER TABLE upload_sessions ADD CONSTRAINT upload_sessions_parent_id_library_id_fkey
    FOREIGN KEY (parent_id, library_id) REFERENCES nodes(id, library_id) ON DELETE RESTRICT;

ALTER TABLE blob_locations DROP CONSTRAINT blob_locations_blob_id_library_id_fkey;
ALTER TABLE blob_locations ADD CONSTRAINT blob_locations_blob_id_library_id_fkey
    FOREIGN KEY (blob_id, library_id) REFERENCES blobs(id, library_id) ON DELETE RESTRICT;

ALTER TABLE file_versions DROP CONSTRAINT file_versions_blob_id_library_id_fkey;
ALTER TABLE file_versions ADD CONSTRAINT file_versions_blob_id_library_id_fkey
    FOREIGN KEY (blob_id, library_id) REFERENCES blobs(id, library_id) ON DELETE RESTRICT;

ALTER TABLE file_versions DROP CONSTRAINT file_versions_node_id_library_id_fkey;
ALTER TABLE file_versions ADD CONSTRAINT file_versions_node_id_library_id_fkey
    FOREIGN KEY (node_id, library_id) REFERENCES nodes(id, library_id) ON DELETE RESTRICT;

ALTER TABLE nodes DROP CONSTRAINT nodes_parent_library_fkey;
ALTER TABLE nodes ADD CONSTRAINT nodes_parent_library_fkey
    FOREIGN KEY (parent_id, library_id) REFERENCES nodes(id, library_id);
