BEGIN;

CREATE TABLE plugin_output_latest (
    plugin_instance_id UUID PRIMARY KEY REFERENCES plugin_instances(id) ON DELETE CASCADE,
    sequence BIGINT NOT NULL,
    source_at TIMESTAMP WITH TIME ZONE NOT NULL,
    published_at TIMESTAMP WITH TIME ZONE NOT NULL,
    values JSONB NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT plugin_output_latest_sequence_check CHECK (sequence > 0),
    CONSTRAINT plugin_output_latest_time_check CHECK (source_at <= published_at),
    CONSTRAINT plugin_output_latest_values_check CHECK (
        jsonb_typeof(values) = 'array'
        AND jsonb_array_length(values) BETWEEN 1 AND 128
    )
);

CREATE INDEX idx_plugin_output_latest_published_at ON plugin_output_latest(published_at DESC);

COMMIT;
