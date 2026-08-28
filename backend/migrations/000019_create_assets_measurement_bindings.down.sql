BEGIN;

DROP TABLE IF EXISTS measurement_binding_inputs;
DROP TABLE IF EXISTS measurement_bindings;
DROP TABLE IF EXISTS assets;
DROP FUNCTION IF EXISTS validate_measurement_binding_input();
DROP FUNCTION IF EXISTS validate_measurement_binding();
DROP FUNCTION IF EXISTS validate_asset_hierarchy();

COMMIT;
