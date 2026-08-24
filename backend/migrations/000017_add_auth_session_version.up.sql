BEGIN;

ALTER TABLE users
    ADD COLUMN session_version BIGINT NOT NULL DEFAULT 0,
    ADD CONSTRAINT users_session_version_check CHECK (session_version >= 0);

COMMIT;
