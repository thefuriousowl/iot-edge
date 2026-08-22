BEGIN;

CREATE TABLE reports (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(100) NOT NULL,
    description TEXT,
    logger_id UUID NOT NULL REFERENCES data_loggers(id) ON DELETE CASCADE,
    timezone VARCHAR(100) NOT NULL,
    mode VARCHAR(20) NOT NULL,
    bucket VARCHAR(20),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT reports_name_key UNIQUE (name),
    CONSTRAINT reports_id_logger_key UNIQUE (id, logger_id),
    CONSTRAINT reports_timezone_check CHECK (NULLIF(BTRIM(timezone), '') IS NOT NULL),
    CONSTRAINT reports_mode_check CHECK (mode IN ('raw', 'aggregate')),
    CONSTRAINT reports_mode_bucket_check CHECK (
        (mode = 'raw' AND bucket IS NULL)
        OR (mode = 'aggregate' AND bucket IN ('1m', '5m', '15m', '1h', '6h', '1d', '1w'))
    )
);

CREATE INDEX idx_reports_logger ON reports(logger_id);
CREATE INDEX idx_reports_mode ON reports(mode);

CREATE TABLE report_columns (
    report_id UUID NOT NULL,
    logger_id UUID NOT NULL,
    tag_id UUID NOT NULL,
    position INTEGER NOT NULL,
    name VARCHAR(100) NOT NULL,
    aggregate VARCHAR(20),
    PRIMARY KEY (report_id, tag_id),
    CONSTRAINT report_columns_report_fkey FOREIGN KEY (report_id, logger_id)
        REFERENCES reports(id, logger_id) ON DELETE CASCADE,
    CONSTRAINT report_columns_logger_tag_fkey FOREIGN KEY (logger_id, tag_id)
        REFERENCES data_logger_tags(logger_id, tag_id) ON DELETE CASCADE,
    CONSTRAINT report_columns_position_key UNIQUE (report_id, position),
    CONSTRAINT report_columns_position_check CHECK (position >= 0),
    CONSTRAINT report_columns_name_check CHECK (NULLIF(BTRIM(name), '') IS NOT NULL),
    CONSTRAINT report_columns_aggregate_check CHECK (
        aggregate IS NULL OR aggregate IN ('min', 'max', 'avg', 'sum', 'count', 'first', 'last')
    )
);

CREATE UNIQUE INDEX idx_report_columns_name_ci ON report_columns(report_id, LOWER(BTRIM(name)));
CREATE INDEX idx_report_columns_tag ON report_columns(tag_id);

COMMIT;
