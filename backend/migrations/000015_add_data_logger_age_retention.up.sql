BEGIN;

ALTER TABLE data_loggers
    ADD COLUMN max_age_seconds BIGINT,
    ADD CONSTRAINT data_loggers_max_age_check CHECK (
        max_age_seconds IS NULL
        OR max_age_seconds BETWEEN 1 AND 315360000
    );

COMMIT;
