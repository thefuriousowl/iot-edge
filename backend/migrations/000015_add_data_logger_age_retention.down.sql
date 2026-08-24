BEGIN;

ALTER TABLE data_loggers
    DROP CONSTRAINT data_loggers_max_age_check,
    DROP COLUMN max_age_seconds;

COMMIT;
