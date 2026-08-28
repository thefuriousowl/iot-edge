BEGIN;

CREATE TABLE assets (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    parent_id UUID REFERENCES assets(id) ON DELETE RESTRICT,
    name VARCHAR(100) NOT NULL,
    kind VARCHAR(32) NOT NULL,
    description TEXT,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    timezone VARCHAR(100),
    position INTEGER NOT NULL DEFAULT 0,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT assets_name_check CHECK (name = BTRIM(name) AND NULLIF(name, '') IS NOT NULL),
    CONSTRAINT assets_kind_check CHECK (kind IN ('site','building','area','system','equipment','meter','custom')),
    CONSTRAINT assets_root_check CHECK ((kind = 'site') = (parent_id IS NULL)),
    CONSTRAINT assets_timezone_check CHECK (timezone IS NULL OR (timezone = BTRIM(timezone) AND NULLIF(timezone, '') IS NOT NULL)),
    CONSTRAINT assets_position_check CHECK (position >= 0),
    CONSTRAINT assets_metadata_check CHECK (jsonb_typeof(metadata) = 'object'),
    CONSTRAINT assets_not_self_parent CHECK (parent_id IS NULL OR parent_id <> id)
);

CREATE UNIQUE INDEX idx_assets_sibling_name_ci ON assets(parent_id, LOWER(name)) NULLS NOT DISTINCT;
CREATE INDEX idx_assets_parent_order ON assets(parent_id, position, LOWER(name), id);
CREATE INDEX idx_assets_kind ON assets(kind);
CREATE INDEX idx_assets_enabled ON assets(enabled);

CREATE FUNCTION validate_asset_hierarchy() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
DECLARE
    ancestor_id UUID;
    ancestor_parent_id UUID;
    hierarchy_depth INTEGER := 1;
BEGIN
    IF NEW.parent_id IS NULL THEN
        RETURN NEW;
    END IF;
    IF NEW.parent_id = NEW.id THEN
        RAISE EXCEPTION 'asset hierarchy cycle' USING ERRCODE = '23514';
    END IF;
    IF to_regclass('measurement_bindings') IS NOT NULL AND EXISTS (
        SELECT 1 FROM measurement_bindings WHERE asset_id = NEW.parent_id
    ) THEN
        RAISE EXCEPTION 'measurement owner must remain a leaf asset' USING ERRCODE = '23514';
    END IF;

    ancestor_id := NEW.parent_id;
    LOOP
        hierarchy_depth := hierarchy_depth + 1;
        IF hierarchy_depth > 16 THEN
            RAISE EXCEPTION 'asset hierarchy depth exceeds 16' USING ERRCODE = '23514';
        END IF;
        IF ancestor_id = NEW.id THEN
            RAISE EXCEPTION 'asset hierarchy cycle' USING ERRCODE = '23514';
        END IF;
        SELECT parent_id INTO ancestor_parent_id FROM assets WHERE id = ancestor_id;
        IF NOT FOUND THEN
            RAISE EXCEPTION 'asset parent does not exist' USING ERRCODE = '23503';
        END IF;
        EXIT WHEN ancestor_parent_id IS NULL;
        ancestor_id := ancestor_parent_id;
    END LOOP;
    RETURN NEW;
END;
$$;

CREATE TRIGGER trg_assets_validate_hierarchy
BEFORE INSERT OR UPDATE OF id, parent_id ON assets
FOR EACH ROW EXECUTE FUNCTION validate_asset_hierarchy();

CREATE TABLE measurement_bindings (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    asset_id UUID NOT NULL REFERENCES assets(id) ON DELETE RESTRICT,
    boundary_asset_id UUID NOT NULL REFERENCES assets(id) ON DELETE RESTRICT,
    source_type VARCHAR(32) NOT NULL,
    tag_id UUID REFERENCES tags(id) ON DELETE RESTRICT,
    plugin_instance_id UUID REFERENCES plugin_instances(id) ON DELETE RESTRICT,
    output_key VARCHAR(128),
    resource VARCHAR(32) NOT NULL,
    quantity VARCHAR(32) NOT NULL,
    unit VARCHAR(32) NOT NULL,
    precision SMALLINT NOT NULL DEFAULT 3,
    reference JSONB NOT NULL DEFAULT '{}'::jsonb,
    meter_role VARCHAR(16) NOT NULL,
    rollup_policy VARCHAR(16) NOT NULL DEFAULT 'include',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT measurement_bindings_source_type_check CHECK (source_type IN ('tag','plugin_output')),
    CONSTRAINT measurement_bindings_source_check CHECK (
        (source_type = 'tag' AND tag_id IS NOT NULL AND plugin_instance_id IS NULL AND output_key IS NULL)
        OR
        (source_type = 'plugin_output' AND tag_id IS NULL AND plugin_instance_id IS NOT NULL AND output_key IS NOT NULL)
    ),
    CONSTRAINT measurement_bindings_output_key_check CHECK (output_key IS NULL OR output_key ~ '^[a-z][a-z0-9_.]{0,127}$'),
    CONSTRAINT measurement_bindings_resource_check CHECK (resource IN ('electricity','thermal','compressed_air','steam','gas','water','solar','custom')),
    CONSTRAINT measurement_bindings_quantity_check CHECK (quantity IN ('power','energy','flow_rate','volume','pressure','temperature','ratio','state','cost')),
    CONSTRAINT measurement_bindings_unit_check CHECK (unit IN ('W','kW','MW','Wh','kWh','MWh','m3','Nm3','m3/s','m3/h','Nm3/h','Pa','kPa','bar','K','degC','degF','1','%','bool')),
    CONSTRAINT measurement_bindings_precision_check CHECK (precision BETWEEN 0 AND 12),
    CONSTRAINT measurement_bindings_reference_check CHECK (jsonb_typeof(reference) = 'object'),
    CONSTRAINT measurement_bindings_meter_role_check CHECK (meter_role IN ('direct','main','submeter','virtual')),
    CONSTRAINT measurement_bindings_rollup_policy_check CHECK (rollup_policy IN ('include','exclude'))
);

CREATE UNIQUE INDEX idx_measurement_bindings_tag_owner ON measurement_bindings(tag_id) WHERE source_type = 'tag';
CREATE UNIQUE INDEX idx_measurement_bindings_plugin_output_owner ON measurement_bindings(plugin_instance_id, output_key) WHERE source_type = 'plugin_output';
CREATE INDEX idx_measurement_bindings_asset ON measurement_bindings(asset_id);
CREATE INDEX idx_measurement_bindings_boundary_semantic ON measurement_bindings(boundary_asset_id, resource, quantity, unit);

CREATE FUNCTION validate_measurement_binding() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
DECLARE
    ancestor_id UUID;
    ancestor_parent_id UUID;
    boundary_found BOOLEAN := FALSE;
    hierarchy_depth INTEGER := 0;
BEGIN
    IF EXISTS (SELECT 1 FROM assets WHERE parent_id = NEW.asset_id) THEN
        RAISE EXCEPTION 'measurement owner must be a leaf asset' USING ERRCODE = '23514';
    END IF;
    ancestor_id := NEW.asset_id;
    LOOP
        hierarchy_depth := hierarchy_depth + 1;
        IF hierarchy_depth > 16 THEN
            RAISE EXCEPTION 'asset hierarchy depth exceeds 16' USING ERRCODE = '23514';
        END IF;
        IF ancestor_id = NEW.boundary_asset_id THEN
            boundary_found := TRUE;
            EXIT;
        END IF;
        SELECT parent_id INTO ancestor_parent_id FROM assets WHERE id = ancestor_id;
        EXIT WHEN NOT FOUND OR ancestor_parent_id IS NULL;
        ancestor_id := ancestor_parent_id;
    END LOOP;
    IF NOT boundary_found THEN
        RAISE EXCEPTION 'measurement boundary must be its owner or an ancestor' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER trg_measurement_bindings_validate
BEFORE INSERT OR UPDATE OF asset_id, boundary_asset_id ON measurement_bindings
FOR EACH ROW EXECUTE FUNCTION validate_measurement_binding();

CREATE TABLE measurement_binding_inputs (
    binding_id UUID NOT NULL REFERENCES measurement_bindings(id) ON DELETE CASCADE,
    input_binding_id UUID NOT NULL REFERENCES measurement_bindings(id) ON DELETE RESTRICT,
    PRIMARY KEY (binding_id, input_binding_id),
    CONSTRAINT measurement_binding_inputs_not_self CHECK (binding_id <> input_binding_id)
);

CREATE INDEX idx_measurement_binding_inputs_input ON measurement_binding_inputs(input_binding_id);

CREATE FUNCTION validate_measurement_binding_input() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
DECLARE
    owner_role VARCHAR(16);
    dependency_cycle BOOLEAN;
BEGIN
    SELECT meter_role INTO owner_role FROM measurement_bindings WHERE id = NEW.binding_id;
    IF owner_role IS DISTINCT FROM 'virtual' THEN
        RAISE EXCEPTION 'only virtual meters may declare inputs' USING ERRCODE = '23514';
    END IF;
    WITH RECURSIVE dependencies(id) AS (
        SELECT NEW.input_binding_id
        UNION
        SELECT inputs.input_binding_id
        FROM measurement_binding_inputs inputs
        JOIN dependencies ON inputs.binding_id = dependencies.id
    )
    SELECT EXISTS (SELECT 1 FROM dependencies WHERE id = NEW.binding_id) INTO dependency_cycle;
    IF dependency_cycle THEN
        RAISE EXCEPTION 'virtual meter dependency cycle' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER trg_measurement_binding_inputs_validate
BEFORE INSERT OR UPDATE OF binding_id, input_binding_id ON measurement_binding_inputs
FOR EACH ROW EXECUTE FUNCTION validate_measurement_binding_input();

COMMIT;
