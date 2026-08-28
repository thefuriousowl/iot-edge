package migrations

import (
	"context"
	"database/sql"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestAssetsMeasurementBindingsMigrationConstraintsAndDown_Integration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run the PostgreSQL migration test")
	}
	ctx := context.Background()
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("opening PostgreSQL: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	connection, err := database.Conn(ctx)
	if err != nil {
		t.Fatalf("getting connection: %v", err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	schema := "assets_migration_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := connection.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("creating schema: %v", err)
	}
	t.Cleanup(func() { _, _ = database.ExecContext(context.Background(), "DROP SCHEMA IF EXISTS "+schema+" CASCADE") })
	if _, err := connection.ExecContext(ctx, "SET search_path TO "+schema+", public"); err != nil {
		t.Fatalf("setting search path: %v", err)
	}
	for _, migration := range []string{
		"000002_create_vgateways.up.sql", "000003_create_devices_datasources.up.sql", "000004_create_tags.up.sql",
		"000010_create_plugin_instances.up.sql", "000019_create_assets_measurement_bindings.up.sql",
	} {
		applyMigrationFile(t, ctx, connection, migration)
	}
	for _, relation := range []string{
		"assets", "measurement_bindings", "measurement_binding_inputs", "idx_assets_sibling_name_ci",
		"idx_assets_parent_order", "idx_measurement_bindings_tag_owner", "idx_measurement_bindings_plugin_output_owner",
	} {
		if !relationExists(t, ctx, connection, schema, relation) {
			t.Errorf("relation %q was not created", relation)
		}
	}

	siteID := insertAsset(t, ctx, connection, nil, "Plant", "site")
	areaID := insertAsset(t, ctx, connection, &siteID, "Utilities", "area")
	meterID := insertAsset(t, ctx, connection, &areaID, "Main meter", "meter")
	secondSiteID := insertAsset(t, ctx, connection, nil, "Other plant", "site")
	equipmentID := insertAsset(t, ctx, connection, &siteID, "Virtual total", "equipment")

	invalidSQL(t, ctx, connection, `INSERT INTO assets (parent_id,name,kind) VALUES ($1,'Nested site','site')`, siteID)
	invalidSQL(t, ctx, connection, `INSERT INTO assets (name,kind) VALUES ('Root meter','meter')`)
	invalidSQL(t, ctx, connection, `UPDATE assets SET parent_id=$1 WHERE id=$2`, meterID, siteID)
	deepParentID := insertAsset(t, ctx, connection, nil, "Deep hierarchy", "site")
	for depth := 2; depth <= 16; depth++ {
		deepParentID = insertAsset(t, ctx, connection, &deepParentID, "Level "+strconv.Itoa(depth), "custom")
	}
	invalidSQL(t, ctx, connection, `INSERT INTO assets (parent_id,name,kind) VALUES ($1,'Level 17','custom')`, deepParentID)

	var tagID uuid.UUID
	if err := connection.QueryRowContext(ctx, `INSERT INTO tags (name,type,data_type,config) VALUES ('Asset power','constant','float64','{"value":1}') RETURNING id`).Scan(&tagID); err != nil {
		t.Fatalf("creating Tag: %v", err)
	}
	var pluginID uuid.UUID
	if err := connection.QueryRowContext(ctx, `INSERT INTO plugin_instances (type,name,config_version) VALUES ('energy_management','Asset Energy',1) RETURNING id`).Scan(&pluginID); err != nil {
		t.Fatalf("creating Plugin: %v", err)
	}

	tagBindingID := insertTagBinding(t, ctx, connection, meterID, siteID, tagID, "main")
	if _, err := connection.ExecContext(ctx, `INSERT INTO assets (parent_id,name,kind) VALUES ($1,'Forbidden child','equipment')`, meterID); err == nil {
		t.Fatal("measurement-owning Asset accepted a child")
	}
	invalidSQL(t, ctx, connection, `INSERT INTO measurement_bindings (asset_id,boundary_asset_id,source_type,tag_id,resource,quantity,unit,meter_role) VALUES ($1,$2,'tag',$3,'electricity','power','kW','direct')`, equipmentID, siteID, tagID)
	invalidSQL(t, ctx, connection, `INSERT INTO measurement_bindings (asset_id,boundary_asset_id,source_type,plugin_instance_id,output_key,resource,quantity,unit,meter_role) VALUES ($1,$2,'plugin_output',$3,'other_power','electricity','power','kW','direct')`, equipmentID, secondSiteID, pluginID)
	invalidSQL(t, ctx, connection, `INSERT INTO measurement_bindings (asset_id,boundary_asset_id,source_type,tag_id,plugin_instance_id,output_key,resource,quantity,unit,meter_role) VALUES ($1,$2,'tag',$3,$4,'electrical_demand_kw','electricity','power','kW','direct')`, equipmentID, siteID, uuid.New(), pluginID)

	var virtualBindingID uuid.UUID
	if err := connection.QueryRowContext(ctx, `
		INSERT INTO measurement_bindings (asset_id,boundary_asset_id,source_type,plugin_instance_id,output_key,resource,quantity,unit,meter_role)
		VALUES ($1,$2,'plugin_output',$3,'electrical_demand_kw','electricity','power','kW','virtual') RETURNING id
	`, equipmentID, siteID, pluginID).Scan(&virtualBindingID); err != nil {
		t.Fatalf("creating Plugin-output binding: %v", err)
	}
	if _, err := connection.ExecContext(ctx, `INSERT INTO measurement_binding_inputs (binding_id,input_binding_id) VALUES ($1,$2)`, virtualBindingID, tagBindingID); err != nil {
		t.Fatalf("creating virtual input: %v", err)
	}
	if _, err := connection.ExecContext(ctx, `UPDATE measurement_bindings SET meter_role='virtual' WHERE id=$1`, tagBindingID); err != nil {
		t.Fatalf("preparing cycle: %v", err)
	}
	invalidSQL(t, ctx, connection, `INSERT INTO measurement_binding_inputs (binding_id,input_binding_id) VALUES ($1,$2)`, tagBindingID, virtualBindingID)

	applyMigrationFile(t, ctx, connection, "000019_create_assets_measurement_bindings.down.sql")
	for _, relation := range []string{"measurement_binding_inputs", "measurement_bindings", "assets"} {
		if relationExists(t, ctx, connection, schema, relation) {
			t.Errorf("relation %q remains after down migration", relation)
		}
	}
	if !relationExists(t, ctx, connection, schema, "tags") || !relationExists(t, ctx, connection, schema, "plugin_instances") {
		t.Error("Asset down migration removed prerequisite tables")
	}
}

func insertAsset(t *testing.T, ctx context.Context, connection *sql.Conn, parentID *uuid.UUID, name, kind string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := connection.QueryRowContext(ctx, `INSERT INTO assets (parent_id,name,kind) VALUES ($1,$2,$3) RETURNING id`, parentID, name, kind).Scan(&id); err != nil {
		t.Fatalf("creating %s Asset: %v", kind, err)
	}
	return id
}

func insertTagBinding(t *testing.T, ctx context.Context, connection *sql.Conn, assetID, boundaryID, tagID uuid.UUID, role string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := connection.QueryRowContext(ctx, `
		INSERT INTO measurement_bindings (asset_id,boundary_asset_id,source_type,tag_id,resource,quantity,unit,meter_role)
		VALUES ($1,$2,'tag',$3,'electricity','power','kW',$4) RETURNING id
	`, assetID, boundaryID, tagID, role).Scan(&id); err != nil {
		t.Fatalf("creating Tag binding: %v", err)
	}
	return id
}

func invalidSQL(t *testing.T, ctx context.Context, connection *sql.Conn, statement string, arguments ...any) {
	t.Helper()
	if _, err := connection.ExecContext(ctx, statement, arguments...); err == nil {
		t.Errorf("invalid statement succeeded: %s", statement)
	}
}
