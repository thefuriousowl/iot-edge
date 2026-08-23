BEGIN;

CREATE TABLE plugin_instances (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    type VARCHAR(64) NOT NULL,
    name VARCHAR(100) NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    config JSONB NOT NULL DEFAULT '{}'::jsonb,
    config_version INTEGER NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT plugin_instances_type_check CHECK (type ~ '^[a-z][a-z0-9_]{0,63}$'),
    CONSTRAINT plugin_instances_name_check CHECK (
        name = BTRIM(name)
        AND NULLIF(name, '') IS NOT NULL
    ),
    CONSTRAINT plugin_instances_config_check CHECK (jsonb_typeof(config) = 'object'),
    CONSTRAINT plugin_instances_config_version_check CHECK (config_version > 0)
);

CREATE UNIQUE INDEX idx_plugin_instances_name_ci ON plugin_instances(LOWER(name));
CREATE INDEX idx_plugin_instances_type ON plugin_instances(type);
CREATE INDEX idx_plugin_instances_enabled ON plugin_instances(enabled);
CREATE INDEX idx_plugin_instances_type_enabled ON plugin_instances(type, enabled);

COMMIT;
