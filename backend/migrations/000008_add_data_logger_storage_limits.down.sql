BEGIN;

DROP TABLE IF EXISTS data_logger_batches;

ALTER TABLE data_loggers
    DROP CONSTRAINT IF EXISTS data_loggers_max_size_check,
    DROP COLUMN IF EXISTS max_size_bytes;

COMMIT;
