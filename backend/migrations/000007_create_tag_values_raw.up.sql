BEGIN;

CREATE TABLE tag_values_raw (
    logger_id UUID NOT NULL REFERENCES data_loggers(id) ON DELETE CASCADE,
    tag_id UUID NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    batch_at TIMESTAMP WITH TIME ZONE NOT NULL,
    observed_at TIMESTAMP WITH TIME ZONE NOT NULL,
    data_type VARCHAR(20) NOT NULL,
    value JSONB,
    quality VARCHAR(20) NOT NULL,
    error_message TEXT,
    persisted_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (logger_id, tag_id, batch_at),
    CONSTRAINT tag_values_raw_data_type_check CHECK (data_type IN ('bool', 'int16', 'uint16', 'int32', 'uint32', 'float32', 'float64')),
    CONSTRAINT tag_values_raw_quality_check CHECK (quality IN ('good', 'bad')),
    CONSTRAINT tag_values_raw_payload_check CHECK (
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
) PARTITION BY RANGE (batch_at);

CREATE TABLE tag_values_raw_default PARTITION OF tag_values_raw DEFAULT;

CREATE INDEX idx_tag_values_raw_logger_batch ON tag_values_raw(logger_id, batch_at DESC);
CREATE INDEX idx_tag_values_raw_tag_batch ON tag_values_raw(tag_id, batch_at DESC);
CREATE INDEX idx_tag_values_raw_batch ON tag_values_raw(batch_at DESC);

COMMIT;
