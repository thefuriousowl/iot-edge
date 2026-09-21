BEGIN;

DROP TRIGGER IF EXISTS trg_energy_measurement_runs_archived_immutable ON energy_measurement_runs;
DROP FUNCTION IF EXISTS protect_archived_energy_measurement_run();
DROP TRIGGER IF EXISTS trg_energy_measurement_runs_plugin_type ON energy_measurement_runs;
DROP FUNCTION IF EXISTS enforce_energy_measurement_run_instance();
DROP TABLE IF EXISTS energy_measurement_runs;

COMMIT;
