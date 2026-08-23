ALTER TABLE data_publishers
    DROP CONSTRAINT IF EXISTS data_publishers_credential_id_fkey,
    DROP CONSTRAINT IF EXISTS data_publishers_secret_revision_check,
    DROP COLUMN IF EXISTS credential_id,
    DROP COLUMN IF EXISTS secret_revision;

DROP TABLE IF EXISTS credential_secrets;
DROP TABLE IF EXISTS credential_profiles;
