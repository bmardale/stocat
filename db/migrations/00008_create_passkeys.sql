-- +goose Up
CREATE TABLE passkey_users (
    user_id BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    handle BYTEA NOT NULL UNIQUE CHECK (octet_length(handle) = 64)
);

CREATE TABLE passkeys (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    public_id TEXT NOT NULL UNIQUE,
    user_id BIGINT NOT NULL REFERENCES passkey_users(user_id) ON DELETE CASCADE,
    rp_id TEXT NOT NULL,
    credential_id BYTEA NOT NULL CHECK (octet_length(credential_id) BETWEEN 1 AND 1023),
    name TEXT NOT NULL,
    public_key BYTEA NOT NULL,
    attestation_type TEXT NOT NULL,
    attestation_format TEXT NOT NULL,
    attestation JSONB NOT NULL,
    extensions JSONB NOT NULL,
    transports TEXT[] NOT NULL,
    aaguid BYTEA,
    attachment TEXT NOT NULL,
    sign_count BIGINT NOT NULL CHECK (sign_count BETWEEN 0 AND 4294967295),
    flags SMALLINT NOT NULL CHECK (flags BETWEEN 0 AND 255),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at TIMESTAMPTZ,
    UNIQUE (rp_id, credential_id)
);

CREATE INDEX passkeys_user_id_idx ON passkeys (user_id, rp_id);

CREATE TABLE webauthn_ceremonies (
    challenge TEXT PRIMARY KEY,
    kind TEXT NOT NULL CHECK (kind IN ('registration', 'login')),
    user_id BIGINT REFERENCES users(id) ON DELETE CASCADE,
    session_data JSONB NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT webauthn_ceremonies_user_check CHECK ((kind = 'registration') = (user_id IS NOT NULL))
);

CREATE INDEX webauthn_ceremonies_expires_at_idx ON webauthn_ceremonies (expires_at);

-- +goose Down
DROP TABLE webauthn_ceremonies;
DROP TABLE passkeys;
DROP TABLE passkey_users;
