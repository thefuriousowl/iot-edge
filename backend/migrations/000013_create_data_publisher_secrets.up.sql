ALTER TABLE data_publishers
    ADD COLUMN secret_revision BIGINT NOT NULL DEFAULT 0,
    ADD CONSTRAINT data_publishers_secret_revision_check CHECK (secret_revision >= 0);

CREATE TABLE data_publisher_secrets (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    publisher_id UUID NOT NULL REFERENCES data_publishers(id) ON DELETE CASCADE,
    name VARCHAR(64) NOT NULL,
    kind VARCHAR(32) NOT NULL,
    key_id VARCHAR(64) NOT NULL,
    ciphertext BYTEA NOT NULL,
    revision BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    rotated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT data_publisher_secrets_name_check CHECK (name ~ '^[a-z][a-z0-9_.-]{0,63}$'),
    CONSTRAINT data_publisher_secrets_kind_check CHECK (kind IN ('opaque', 'ca_certificate', 'client_identity')),
    CONSTRAINT data_publisher_secrets_key_id_check CHECK (key_id = BTRIM(key_id) AND LENGTH(key_id) BETWEEN 1 AND 64),
    CONSTRAINT data_publisher_secrets_ciphertext_check CHECK (OCTET_LENGTH(ciphertext) BETWEEN 29 AND 262176),
    CONSTRAINT data_publisher_secrets_revision_check CHECK (revision > 0),
    CONSTRAINT data_publisher_secrets_rotation_check CHECK (rotated_at >= created_at),
    CONSTRAINT data_publisher_secrets_publisher_name_key UNIQUE (publisher_id, name)
);

CREATE INDEX idx_data_publisher_secrets_publisher ON data_publisher_secrets(publisher_id);
