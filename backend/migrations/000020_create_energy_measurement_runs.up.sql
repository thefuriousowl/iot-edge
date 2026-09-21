BEGIN;

CREATE TABLE energy_measurement_runs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    plugin_instance_id UUID NOT NULL REFERENCES plugin_instances(id) ON DELETE RESTRICT,
    name VARCHAR(100) NOT NULL,
    reason VARCHAR(500) NOT NULL DEFAULT '',
    status VARCHAR(16) NOT NULL,
    started_at TIMESTAMP WITH TIME ZONE NOT NULL,
    ended_at TIMESTAMP WITH TIME ZONE,
    config_version INTEGER NOT NULL,
    config_snapshot JSONB NOT NULL,
    archive_payload JSONB,
    archive_sha256 CHAR(64),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    archived_at TIMESTAMP WITH TIME ZONE,
    CONSTRAINT energy_measurement_runs_name_check CHECK (
        name = BTRIM(name) AND NULLIF(name, '') IS NOT NULL
    ),
    CONSTRAINT energy_measurement_runs_reason_check CHECK (reason = BTRIM(reason)),
    CONSTRAINT energy_measurement_runs_status_check CHECK (status IN ('active', 'archived')),
    CONSTRAINT energy_measurement_runs_config_version_check CHECK (config_version > 0),
    CONSTRAINT energy_measurement_runs_config_snapshot_check CHECK (jsonb_typeof(config_snapshot) = 'object'),
    CONSTRAINT energy_measurement_runs_archive_payload_check CHECK (
        archive_payload IS NULL OR jsonb_typeof(archive_payload) = 'object'
    ),
    CONSTRAINT energy_measurement_runs_archive_sha256_check CHECK (
        archive_sha256 IS NULL OR archive_sha256 ~ '^[0-9a-f]{64}$'
    ),
    CONSTRAINT energy_measurement_runs_state_check CHECK (
        (status = 'active' AND ended_at IS NULL AND archived_at IS NULL AND archive_payload IS NULL AND archive_sha256 IS NULL)
        OR
        (status = 'archived' AND ended_at IS NOT NULL AND ended_at >= started_at AND archived_at IS NOT NULL AND archive_payload IS NOT NULL AND archive_sha256 IS NOT NULL)
    )
);

CREATE UNIQUE INDEX idx_energy_measurement_runs_active
    ON energy_measurement_runs(plugin_instance_id)
    WHERE status = 'active';
CREATE INDEX idx_energy_measurement_runs_archive
    ON energy_measurement_runs(plugin_instance_id, ended_at DESC, id DESC)
    WHERE status = 'archived';

CREATE FUNCTION enforce_energy_measurement_run_instance() RETURNS TRIGGER AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM plugin_instances
        WHERE id = NEW.plugin_instance_id AND type = 'energy_management'
    ) THEN
        RAISE EXCEPTION 'measurement run requires an Energy Management Plugin instance'
            USING ERRCODE = '23514', CONSTRAINT = 'energy_measurement_runs_plugin_type_check';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_energy_measurement_runs_plugin_type
    BEFORE INSERT OR UPDATE OF plugin_instance_id ON energy_measurement_runs
    FOR EACH ROW EXECUTE FUNCTION enforce_energy_measurement_run_instance();

CREATE FUNCTION protect_archived_energy_measurement_run() RETURNS TRIGGER AS $$
BEGIN
    IF OLD.status = 'archived' THEN
        RAISE EXCEPTION 'archived Energy measurement runs are immutable'
            USING ERRCODE = '23514', CONSTRAINT = 'energy_measurement_runs_archived_immutable';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_energy_measurement_runs_archived_immutable
    BEFORE UPDATE OR DELETE ON energy_measurement_runs
    FOR EACH ROW EXECUTE FUNCTION protect_archived_energy_measurement_run();

COMMIT;
