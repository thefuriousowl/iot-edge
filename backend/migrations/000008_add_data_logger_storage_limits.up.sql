BEGIN;

ALTER TABLE data_loggers
    ADD COLUMN max_size_bytes BIGINT,
    ADD CONSTRAINT data_loggers_max_size_check CHECK (
        max_size_bytes IS NULL
        OR max_size_bytes BETWEEN 1048576 AND 1099511627776
    );

CREATE TABLE data_logger_batches (
    logger_id UUID NOT NULL REFERENCES data_loggers(id) ON DELETE CASCADE,
    batch_at TIMESTAMP WITH TIME ZONE NOT NULL,
    row_count INTEGER NOT NULL,
    estimated_size_bytes BIGINT NOT NULL,
    PRIMARY KEY (logger_id, batch_at),
    CONSTRAINT data_logger_batches_row_count_check CHECK (row_count > 0),
    CONSTRAINT data_logger_batches_size_check CHECK (estimated_size_bytes > 0)
);

INSERT INTO data_logger_batches (logger_id, batch_at, row_count, estimated_size_bytes)
SELECT
    raw_values.logger_id,
    raw_values.batch_at,
    COUNT(*)::INTEGER,
    SUM(pg_column_size(raw_values) + 192)::BIGINT
FROM tag_values_raw AS raw_values
GROUP BY raw_values.logger_id, raw_values.batch_at;

COMMIT;
