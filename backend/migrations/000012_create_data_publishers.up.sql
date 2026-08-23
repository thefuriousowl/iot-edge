BEGIN;

CREATE TABLE data_publishers (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    type VARCHAR(32) NOT NULL,
    name VARCHAR(100) NOT NULL,
    description TEXT,
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    config JSONB NOT NULL DEFAULT '{}'::jsonb,
    config_version INTEGER NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT data_publishers_type_check CHECK (type IN ('http_server', 'mqtt', 'modbus_tcp_server')),
    CONSTRAINT data_publishers_name_check CHECK (
        name = BTRIM(name)
        AND NULLIF(name, '') IS NOT NULL
    ),
    CONSTRAINT data_publishers_description_check CHECK (
        description IS NULL
        OR (description = BTRIM(description) AND NULLIF(description, '') IS NOT NULL)
    ),
    CONSTRAINT data_publishers_config_check CHECK (jsonb_typeof(config) = 'object'),
    CONSTRAINT data_publishers_config_version_check CHECK (config_version > 0)
);

CREATE UNIQUE INDEX idx_data_publishers_name_ci ON data_publishers(LOWER(name));
CREATE INDEX idx_data_publishers_type ON data_publishers(type);
CREATE INDEX idx_data_publishers_enabled ON data_publishers(enabled);
CREATE INDEX idx_data_publishers_type_enabled ON data_publishers(type, enabled);

CREATE TABLE data_publisher_sources (
    publisher_id UUID NOT NULL REFERENCES data_publishers(id) ON DELETE CASCADE,
    position SMALLINT NOT NULL,
    alias VARCHAR(64) NOT NULL,
    kind VARCHAR(20) NOT NULL,
    tag_id UUID REFERENCES tags(id) ON DELETE RESTRICT,
    plugin_instance_id UUID REFERENCES plugin_instances(id) ON DELETE RESTRICT,
    output_key VARCHAR(64),
    PRIMARY KEY (publisher_id, position),
    CONSTRAINT data_publisher_sources_alias_key UNIQUE (publisher_id, alias),
    CONSTRAINT data_publisher_sources_position_check CHECK (position >= 0 AND position < 256),
    CONSTRAINT data_publisher_sources_alias_check CHECK (
        alias = BTRIM(alias)
        AND alias ~ '^[A-Za-z_][A-Za-z0-9_.-]{0,63}$'
    ),
    CONSTRAINT data_publisher_sources_kind_check CHECK (kind IN ('tag', 'plugin_output')),
    CONSTRAINT data_publisher_sources_shape_check CHECK (
        (
            kind = 'tag'
            AND tag_id IS NOT NULL
            AND plugin_instance_id IS NULL
            AND output_key IS NULL
        )
        OR
        (
            kind = 'plugin_output'
            AND tag_id IS NULL
            AND plugin_instance_id IS NOT NULL
            AND output_key IS NOT NULL
            AND output_key ~ '^[a-z][a-z0-9_.-]{0,63}$'
        )
    )
);

CREATE UNIQUE INDEX idx_data_publisher_sources_tag_unique
    ON data_publisher_sources(publisher_id, tag_id)
    WHERE kind = 'tag';
CREATE UNIQUE INDEX idx_data_publisher_sources_plugin_output_unique
    ON data_publisher_sources(publisher_id, plugin_instance_id, output_key)
    WHERE kind = 'plugin_output';
CREATE INDEX idx_data_publisher_sources_tag ON data_publisher_sources(tag_id) WHERE tag_id IS NOT NULL;
CREATE INDEX idx_data_publisher_sources_plugin ON data_publisher_sources(plugin_instance_id) WHERE plugin_instance_id IS NOT NULL;

COMMIT;
