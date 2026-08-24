BEGIN;

ALTER TABLE reports
    DROP CONSTRAINT reports_mode_bucket_check,
    ADD CONSTRAINT reports_mode_bucket_check CHECK (
        (mode = 'raw' AND bucket IS NULL)
        OR (mode = 'aggregate' AND bucket IN ('1m', '5m', '15m', '1h', '6h', '1d', '1w'))
    );

COMMIT;
