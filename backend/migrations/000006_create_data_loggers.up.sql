BEGIN;

CREATE TABLE data_loggers (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(100) NOT NULL,
    description TEXT,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    timezone VARCHAR(100) NOT NULL,
    mode VARCHAR(20) NOT NULL,
    start_at TIMESTAMP WITH TIME ZONE NOT NULL,
    end_at TIMESTAMP WITH TIME ZONE,
    config JSONB NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT data_loggers_name_key UNIQUE (name),
    CONSTRAINT data_loggers_mode_check CHECK (mode IN ('interval', 'schedule')),
    CONSTRAINT data_loggers_end_check CHECK (end_at IS NULL OR end_at > start_at),
    CONSTRAINT data_loggers_config_check CHECK (jsonb_typeof(config) = 'object'),
    CONSTRAINT data_loggers_mode_config_check CHECK (
        (
            mode = 'interval'
            AND config ? 'interval_seconds'
            AND jsonb_typeof(config->'interval_seconds') = 'number'
            AND (config->>'interval_seconds')::NUMERIC = TRUNC((config->>'interval_seconds')::NUMERIC)
            AND (config->>'interval_seconds')::NUMERIC > 0
            AND (config->>'interval_seconds')::NUMERIC <= 9223372036
        )
        OR (
            mode = 'schedule'
            AND config ? 'unit'
            AND config->>'unit' IN ('minute', 'hour', 'day', 'week')
            AND config ? 'every'
            AND jsonb_typeof(config->'every') = 'number'
            AND (config->>'every')::NUMERIC = TRUNC((config->>'every')::NUMERIC)
            AND (config->>'every')::NUMERIC > 0
            AND (config->>'every')::NUMERIC <= 2562047
        )
    )
);

CREATE INDEX idx_data_loggers_enabled ON data_loggers(enabled);
CREATE INDEX idx_data_loggers_mode ON data_loggers(mode);
CREATE INDEX idx_data_loggers_start_at ON data_loggers(start_at);

CREATE TABLE data_logger_tags (
    logger_id UUID NOT NULL REFERENCES data_loggers(id) ON DELETE CASCADE,
    tag_id UUID NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    position INTEGER NOT NULL,
    PRIMARY KEY (logger_id, tag_id),
    CONSTRAINT data_logger_tags_position_key UNIQUE (logger_id, position),
    CONSTRAINT data_logger_tags_position_check CHECK (position >= 0)
);

CREATE INDEX idx_data_logger_tags_tag ON data_logger_tags(tag_id);

COMMIT;
