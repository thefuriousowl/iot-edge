BEGIN;

CREATE TABLE tag_values_latest (
    tag_id UUID PRIMARY KEY REFERENCES tags(id) ON DELETE CASCADE,
    sequence BIGINT NOT NULL,
    data_type VARCHAR(20) NOT NULL,
    value JSONB,
    quality VARCHAR(20) NOT NULL,
    observed_at TIMESTAMP WITH TIME ZONE NOT NULL,
    stored_at TIMESTAMP WITH TIME ZONE NOT NULL,
    error_message TEXT,
    persisted_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT tag_values_latest_sequence_key UNIQUE (sequence),
    CONSTRAINT tag_values_latest_sequence_check CHECK (sequence > 0),
    CONSTRAINT tag_values_latest_data_type_check CHECK (data_type IN ('bool', 'int16', 'uint16', 'int32', 'uint32', 'float32', 'float64')),
    CONSTRAINT tag_values_latest_quality_check CHECK (quality IN ('good', 'bad')),
    CONSTRAINT tag_values_latest_payload_check CHECK (
        (
            quality = 'good'
            AND error_message IS NULL
            AND (
                (data_type = 'bool' AND jsonb_typeof(value) = 'boolean')
                OR (
                    data_type IN ('int16', 'uint16', 'int32', 'uint32')
                    AND jsonb_typeof(value) = 'number'
                    AND (value #>> '{}')::NUMERIC = TRUNC((value #>> '{}')::NUMERIC)
                    AND (
                        (data_type = 'int16' AND (value #>> '{}')::NUMERIC BETWEEN -32768 AND 32767)
                        OR (data_type = 'uint16' AND (value #>> '{}')::NUMERIC BETWEEN 0 AND 65535)
                        OR (data_type = 'int32' AND (value #>> '{}')::NUMERIC BETWEEN -2147483648 AND 2147483647)
                        OR (data_type = 'uint32' AND (value #>> '{}')::NUMERIC BETWEEN 0 AND 4294967295)
                    )
                )
                OR (data_type IN ('float32', 'float64') AND jsonb_typeof(value) = 'number')
            )
        )
        OR (
            quality = 'bad'
            AND value IS NULL
            AND NULLIF(BTRIM(error_message), '') IS NOT NULL
        )
    )
);

CREATE INDEX idx_tag_values_latest_observed_at ON tag_values_latest(observed_at DESC);

COMMIT;
