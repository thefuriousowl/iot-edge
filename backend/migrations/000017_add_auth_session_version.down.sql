BEGIN;

ALTER TABLE users
    DROP CONSTRAINT IF EXISTS users_session_version_check,
    DROP COLUMN IF EXISTS session_version;

COMMIT;
