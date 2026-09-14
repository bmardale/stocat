DO $$
DECLARE
    owner BIGINT;
    backend BIGINT;
    library BIGINT;
    other_library BIGINT;
    root BIGINT;
    other_root BIGINT;
    child BIGINT;
    metadata BYTEA := decode(repeat('00', 16), 'hex');
    envelope BYTEA := decode(repeat('00', 48), 'hex');
    token BYTEA := decode(repeat('00', 32), 'hex');
    signature BYTEA := decode(repeat('00', 64), 'hex');
    deployment UUID;
BEGIN
    SELECT public_id INTO STRICT deployment FROM encryption_deployment;
    BEGIN
        UPDATE encryption_deployment SET public_id = gen_random_uuid();
        RAISE EXCEPTION 'Deployment update passed.' USING ERRCODE = 'XX000';
    EXCEPTION WHEN raise_exception THEN NULL;
    END;
    BEGIN
        DELETE FROM encryption_deployment;
        RAISE EXCEPTION 'Deployment deletion passed.' USING ERRCODE = 'XX000';
    EXCEPTION WHEN raise_exception THEN NULL;
    END;
    BEGIN
        TRUNCATE encryption_deployment;
        RAISE EXCEPTION 'Deployment truncation passed.' USING ERRCODE = 'XX000';
    EXCEPTION WHEN raise_exception THEN NULL;
    END;
    IF deployment <> (SELECT public_id FROM encryption_deployment) THEN
        RAISE EXCEPTION 'Deployment identifier changed.';
    END IF;
    INSERT INTO users(public_id, name, email, password_hash)
        VALUES ('review-user', 'Review', 'review@example.com', 'unused') RETURNING id INTO owner;
    INSERT INTO storage_backends(public_id, name, type, config, encrypted_secrets)
        VALUES ('review-backend', 'Review', 'local', '{}', 'unused') RETURNING id INTO backend;
    root := nextval('nodes_id_seq');
    other_root := nextval('nodes_id_seq');
    INSERT INTO libraries(public_id, owner_id, backend_id, root_node_id, encryption_mode,
        encryption_format, encrypted_root_metadata, owner_root_envelope)
        VALUES ('review-library', owner, backend, root, 'e2ee', 'v2', metadata, envelope) RETURNING id INTO library;
    INSERT INTO libraries(public_id, owner_id, backend_id, root_node_id, encryption_mode,
        encryption_format, encrypted_root_metadata, owner_root_envelope)
        VALUES ('other-library', owner, backend, other_root, 'e2ee', 'v2', metadata, envelope) RETURNING id INTO other_library;
    INSERT INTO nodes(id, public_id, library_id, kind) OVERRIDING SYSTEM VALUE
        VALUES (root, 'review-root', library, 'folder'), (other_root, 'other-root', other_library, 'folder');
    INSERT INTO nodes(public_id, library_id, parent_id, kind, encrypted_metadata, name_token)
        VALUES ('review-child', library, root, 'file', metadata, token) RETURNING id INTO child;
    BEGIN
        INSERT INTO nodes(public_id, library_id, parent_id, kind, encrypted_metadata, name_token)
            VALUES ('duplicate-child', library, root, 'file', metadata, token);
        RAISE EXCEPTION 'Duplicate sibling token passed.';
    EXCEPTION WHEN unique_violation THEN NULL;
    END;
    BEGIN
        UPDATE libraries SET name = 'plaintext title' WHERE id = library;
        RAISE EXCEPTION 'V2 plaintext title passed.';
    EXCEPTION WHEN check_violation THEN NULL;
    END;
    INSERT INTO node_key_envelopes(child_node_id, parent_node_id, library_id, child_epoch, parent_epoch,
        generation, ciphertext, owner_signature) VALUES (child, root, library, 1, 1, 1, envelope, signature);
    BEGIN
        INSERT INTO node_key_envelopes(child_node_id, parent_node_id, library_id, child_epoch, parent_epoch,
            generation, ciphertext, owner_signature) VALUES (child, other_root, library, 2, 1, 2, envelope, signature);
        RAISE EXCEPTION 'Cross-library envelope passed.';
    EXCEPTION WHEN foreign_key_violation THEN NULL;
    END;
    INSERT INTO blobs(public_id, library_id, size_bytes, ciphertext_sha256, encryption_format)
        VALUES ('review-blob', library, 80, token, 'stocat-framed-v2');
    BEGIN
        UPDATE blobs SET encrypted_file_key = envelope WHERE public_id = 'review-blob';
        RAISE EXCEPTION 'V2 blob key passed.';
    EXCEPTION WHEN check_violation THEN NULL;
    END;
    BEGIN
        UPDATE blobs SET dedup_fingerprint = token WHERE public_id = 'review-blob';
        RAISE EXCEPTION 'V2 fingerprint passed.';
    EXCEPTION WHEN check_violation THEN NULL;
    END;
    BEGIN
        INSERT INTO blobs(public_id, library_id, size_bytes, ciphertext_sha256)
            VALUES ('invalid-plain-blob', library, 0, token);
        RAISE EXCEPTION 'Legacy blob without fingerprint passed.';
    EXCEPTION WHEN check_violation THEN NULL;
    END;
    BEGIN
        INSERT INTO nodes(public_id, library_id, parent_id, kind, encrypted_metadata, name_token)
            VALUES ('oversized-child', library, root, 'file', decode(repeat('00', 65537), 'hex'), token);
        RAISE EXCEPTION 'Oversized metadata passed.';
    EXCEPTION WHEN check_violation THEN NULL;
    END;
END;
$$;
