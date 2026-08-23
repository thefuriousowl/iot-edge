DROP TABLE IF EXISTS data_publisher_secrets;

ALTER TABLE data_publishers
    DROP CONSTRAINT IF EXISTS data_publishers_secret_revision_check,
    DROP COLUMN IF EXISTS secret_revision;
