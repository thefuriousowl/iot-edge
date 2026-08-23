CREATE TABLE credential_profiles (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    type VARCHAR(32) NOT NULL,
    name VARCHAR(100) NOT NULL,
    description TEXT,
    secret_revision BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT credential_profiles_type_check CHECK (type IN ('mqtt')),
    CONSTRAINT credential_profiles_name_check CHECK (name = BTRIM(name) AND NULLIF(name, '') IS NOT NULL),
    CONSTRAINT credential_profiles_description_check CHECK (description IS NULL OR (description = BTRIM(description) AND NULLIF(description, '') IS NOT NULL)),
    CONSTRAINT credential_profiles_secret_revision_check CHECK (secret_revision >= 0)
);

CREATE UNIQUE INDEX idx_credential_profiles_name_ci ON credential_profiles(LOWER(name));
CREATE INDEX idx_credential_profiles_type ON credential_profiles(type);

ALTER TABLE data_publishers
    ADD COLUMN credential_id UUID REFERENCES credential_profiles(id) ON DELETE RESTRICT,
    ADD COLUMN secret_revision BIGINT NOT NULL DEFAULT 0,
    ADD CONSTRAINT data_publishers_secret_revision_check CHECK (secret_revision >= 0);

CREATE INDEX idx_data_publishers_credential ON data_publishers(credential_id) WHERE credential_id IS NOT NULL;

CREATE TABLE credential_secrets (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    credential_id UUID NOT NULL REFERENCES credential_profiles(id) ON DELETE CASCADE,
    name VARCHAR(64) NOT NULL,
    kind VARCHAR(32) NOT NULL,
    key_id VARCHAR(64) NOT NULL,
    ciphertext BYTEA NOT NULL,
    revision BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    rotated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT credential_secrets_name_check CHECK (name IN ('mqtt.username', 'mqtt.password', 'mqtt.custom_ca', 'mqtt.client_identity')),
    CONSTRAINT credential_secrets_kind_check CHECK (
        (name IN ('mqtt.username', 'mqtt.password') AND kind = 'opaque')
        OR (name = 'mqtt.custom_ca' AND kind = 'ca_certificate')
        OR (name = 'mqtt.client_identity' AND kind = 'client_identity')
    ),
    CONSTRAINT credential_secrets_key_id_check CHECK (key_id = BTRIM(key_id) AND LENGTH(key_id) BETWEEN 1 AND 64),
    CONSTRAINT credential_secrets_ciphertext_check CHECK (OCTET_LENGTH(ciphertext) BETWEEN 29 AND 262176),
    CONSTRAINT credential_secrets_revision_check CHECK (revision > 0),
    CONSTRAINT credential_secrets_rotation_check CHECK (rotated_at >= created_at),
    CONSTRAINT credential_secrets_credential_name_key UNIQUE (credential_id, name)
);

CREATE INDEX idx_credential_secrets_credential ON credential_secrets(credential_id);
