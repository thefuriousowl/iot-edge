BEGIN;

CREATE TABLE data_logger_retention_status (
    logger_id UUID PRIMARY KEY REFERENCES data_loggers(id) ON DELETE CASCADE,
    last_started_at TIMESTAMP WITH TIME ZONE NOT NULL,
    last_completed_at TIMESTAMP WITH TIME ZONE NOT NULL,
    last_result JSONB,
    last_error TEXT,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT data_logger_retention_status_time_check CHECK (
        last_completed_at >= last_started_at
    ),
    CONSTRAINT data_logger_retention_status_outcome_check CHECK (
        (last_result IS NULL) <> (last_error IS NULL)
    ),
    CONSTRAINT data_logger_retention_status_result_check CHECK (
        last_result IS NULL OR jsonb_typeof(last_result) = 'object'
    ),
    CONSTRAINT data_logger_retention_status_error_check CHECK (
        last_error IS NULL OR length(btrim(last_error)) > 0
    )
);

COMMIT;
