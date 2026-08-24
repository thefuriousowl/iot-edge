package devicepostgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/thefuriousowl/iot-edge/internal/device"
	gormpostgres "gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestRepositoryDeviceDatasourceCRUDCountsAndCascades_Integration(t *testing.T) {
	database := newDeviceRepositoryDatabase(t)
	repository := NewRepository(database)
	ctx := context.Background()
	gatewayA := insertDeviceGateway(t, database, "Factory A", true)
	gatewayB := insertDeviceGateway(t, database, "Factory B", false)
	description := "Main electrical meter"

	deviceA := &device.Device{
		VGatewayID:  gatewayA,
		Name:        "Meter",
		Type:        device.DeviceTypeModbus,
		Description: &description,
		Enabled:     true,
		Config:      json.RawMessage(`{"unit_id":7}`),
	}
	if err := repository.CreateDevice(ctx, deviceA); err != nil {
		t.Fatalf("CreateDevice() error = %v", err)
	}
	if deviceA.ID == uuid.Nil || deviceA.CreatedAt.IsZero() || deviceA.UpdatedAt.IsZero() {
		t.Fatalf("created Device = %#v", deviceA)
	}
	deviceB := &device.Device{VGatewayID: gatewayB, Name: "Meter", Type: device.DeviceTypeModbus, Enabled: true, Config: json.RawMessage(`{"unit_id":8}`)}
	if err := repository.CreateDevice(ctx, deviceB); err != nil {
		t.Fatalf("CreateDevice(same name, other gateway) error = %v", err)
	}
	duplicateDevice := &device.Device{VGatewayID: gatewayA, Name: "Meter", Type: device.DeviceTypeModbus, Config: json.RawMessage(`{}`)}
	if err := repository.CreateDevice(ctx, duplicateDevice); !errors.Is(err, device.ErrDeviceNameExists) {
		t.Fatalf("CreateDevice(duplicate) error = %v, want %v", err, device.ErrDeviceNameExists)
	}

	foundDevice, err := repository.FindDevice(ctx, deviceA.ID)
	if err != nil {
		t.Fatalf("FindDevice() error = %v", err)
	}
	if foundDevice.ID != deviceA.ID || foundDevice.Gateway.ID != gatewayA || foundDevice.Gateway.Type != "modbus_tcp" || !foundDevice.Gateway.Enabled || foundDevice.Description == nil || *foundDevice.Description != description {
		t.Fatalf("FindDevice() = %#v", foundDevice)
	}

	datasourceA := &device.Datasource{
		DeviceID: deviceA.ID,
		Name:     "Voltage",
		Type:     device.DatasourceTypeModbusRead,
		Enabled:  true,
		Config:   json.RawMessage(`{"function_code":3,"start_address":0,"quantity":2}`),
	}
	if err := repository.CreateDatasource(ctx, datasourceA); err != nil {
		t.Fatalf("CreateDatasource() error = %v", err)
	}
	datasourceB := &device.Datasource{DeviceID: deviceA.ID, Name: "Current", Type: device.DatasourceTypeModbusRead, Enabled: true, Config: json.RawMessage(`{"function_code":3,"start_address":2,"quantity":2}`)}
	if err := repository.CreateDatasource(ctx, datasourceB); err != nil {
		t.Fatalf("CreateDatasource(second) error = %v", err)
	}
	duplicateDatasource := &device.Datasource{DeviceID: deviceA.ID, Name: "Voltage", Type: device.DatasourceTypeModbusRead, Config: json.RawMessage(`{}`)}
	if err := repository.CreateDatasource(ctx, duplicateDatasource); !errors.Is(err, device.ErrDatasourceNameExists) {
		t.Fatalf("CreateDatasource(duplicate) error = %v, want %v", err, device.ErrDatasourceNameExists)
	}
	tagA := insertReadingTag(t, database, datasourceA.ID, "Voltage A")
	insertReadingTag(t, database, datasourceA.ID, "Voltage B")
	insertReadingTag(t, database, datasourceB.ID, "Current A")

	foundDatasource, err := repository.FindDatasource(ctx, datasourceA.ID)
	if err != nil {
		t.Fatalf("FindDatasource() error = %v", err)
	}
	if foundDatasource.ID != datasourceA.ID || foundDatasource.Device.ID != deviceA.ID || foundDatasource.Device.Gateway.ID != gatewayA {
		t.Fatalf("FindDatasource() = %#v", foundDatasource)
	}
	datasources, err := repository.ListDatasources(ctx, deviceA.ID)
	if err != nil {
		t.Fatalf("ListDatasources() error = %v", err)
	}
	if len(datasources) != 2 || datasources[0].ID != datasourceA.ID || datasources[1].ID != datasourceB.ID {
		t.Fatalf("ListDatasources() = %#v", datasources)
	}

	devices, err := repository.ListDevices(ctx, gatewayA)
	if err != nil {
		t.Fatalf("ListDevices() error = %v", err)
	}
	if len(devices) != 1 || devices[0].ID != deviceA.ID || devices[0].DatasourceCount != 2 || devices[0].TagCount != 3 {
		t.Fatalf("ListDevices() = %#v", devices)
	}
	counts, err := repository.CountByVGatewayIDs(ctx, []uuid.UUID{gatewayA, gatewayB, uuid.New()})
	if err != nil {
		t.Fatalf("CountByVGatewayIDs() error = %v", err)
	}
	if counts[gatewayA] != 1 || counts[gatewayB] != 1 || len(counts) != 2 {
		t.Fatalf("CountByVGatewayIDs() = %#v", counts)
	}
	emptyCounts, err := repository.CountByVGatewayIDs(ctx, nil)
	if err != nil || len(emptyCounts) != 0 {
		t.Fatalf("CountByVGatewayIDs(nil) = %#v, %v", emptyCounts, err)
	}

	originalDeviceType := deviceA.Type
	originalGatewayID := deviceA.VGatewayID
	deviceA.Name = "Meter Updated"
	deviceA.Description = nil
	deviceA.Enabled = false
	deviceA.Type = "immutable_type"
	deviceA.VGatewayID = gatewayB
	deviceA.Config = json.RawMessage(`{"unit_id":9}`)
	deviceA.UpdatedAt = time.Time{}
	if err := repository.UpdateDevice(ctx, deviceA); err != nil {
		t.Fatalf("UpdateDevice() error = %v", err)
	}
	updatedDevice, err := repository.FindDevice(ctx, deviceA.ID)
	if err != nil {
		t.Fatalf("FindDevice(updated) error = %v", err)
	}
	if updatedDevice.Name != "Meter Updated" || updatedDevice.Description != nil || updatedDevice.Enabled || updatedDevice.Type != originalDeviceType || updatedDevice.VGatewayID != originalGatewayID || updatedDevice.UpdatedAt.IsZero() {
		t.Fatalf("updated Device = %#v", updatedDevice)
	}

	originalDatasourceType := datasourceA.Type
	originalDeviceID := datasourceA.DeviceID
	datasourceA.Name = "Voltage Updated"
	datasourceA.Enabled = false
	datasourceA.Type = "immutable_type"
	datasourceA.DeviceID = deviceB.ID
	datasourceA.Config = json.RawMessage(`{"function_code":4,"start_address":10,"quantity":2}`)
	datasourceA.UpdatedAt = time.Time{}
	if err := repository.UpdateDatasource(ctx, datasourceA); err != nil {
		t.Fatalf("UpdateDatasource() error = %v", err)
	}
	updatedDatasource, err := repository.FindDatasource(ctx, datasourceA.ID)
	if err != nil {
		t.Fatalf("FindDatasource(updated) error = %v", err)
	}
	if updatedDatasource.Name != "Voltage Updated" || updatedDatasource.Enabled || updatedDatasource.Type != originalDatasourceType || updatedDatasource.DeviceID != originalDeviceID || updatedDatasource.UpdatedAt.IsZero() {
		t.Fatalf("updated Datasource = %#v", updatedDatasource)
	}

	updatedDatasource.Name = datasourceB.Name
	updatedDatasource.Description = stringPointer("must roll back")
	if err := repository.UpdateDatasource(ctx, &updatedDatasource.Datasource); !errors.Is(err, device.ErrDatasourceNameExists) {
		t.Fatalf("UpdateDatasource(duplicate) error = %v", err)
	}
	rolledBackDatasource, err := repository.FindDatasource(ctx, datasourceA.ID)
	if err != nil || rolledBackDatasource.Name != "Voltage Updated" || rolledBackDatasource.Description != nil {
		t.Fatalf("Datasource after failed update = %#v, %v", rolledBackDatasource, err)
	}

	if err := repository.DeleteDatasource(ctx, datasourceA.ID); err != nil {
		t.Fatalf("DeleteDatasource() error = %v", err)
	}
	assertDeviceRowCount(t, database, "tags", "id", tagA, 0)
	if err := repository.DeleteDevice(ctx, deviceA.ID); err != nil {
		t.Fatalf("DeleteDevice() error = %v", err)
	}
	assertDeviceRowCount(t, database, "datasources", "id", datasourceB.ID, 0)
	assertDeviceRowCount(t, database, "tags", "datasource_id", datasourceB.ID, 0)
}

func TestRepositoryInventoryFiltersSearchPaginationAndCounts_Integration(t *testing.T) {
	database := newDeviceRepositoryDatabase(t)
	repository := NewRepository(database)
	ctx := context.Background()
	gatewayPercent := insertDeviceGateway(t, database, "Plant % North", true)
	gatewayUnderscore := insertDeviceGateway(t, database, "Plant_South", false)
	gatewayBackslash := insertDeviceGateway(t, database, `Plant\West`, true)

	alpha := insertDevice(t, repository, gatewayPercent, "Alpha Meter", true)
	percent := insertDevice(t, repository, gatewayPercent, "Meter % Exact", false)
	underscore := insertDevice(t, repository, gatewayUnderscore, "Meter_Exact", true)
	backslash := insertDevice(t, repository, gatewayBackslash, `Meter\Exact`, true)
	beta := insertDevice(t, repository, gatewayPercent, "beta Meter", true)

	alphaSource := insertDatasource(t, repository, alpha.ID, "Alpha Source")
	insertDatasource(t, repository, alpha.ID, "Backup Source")
	insertReadingTag(t, database, alphaSource.ID, "Alpha Voltage")
	insertReadingTag(t, database, alphaSource.ID, "Alpha Current")

	assertDeviceInventory(t, repository, device.DeviceInventoryInput{Search: "%", Page: 1, PerPage: 20}, []uuid.UUID{alpha.ID, beta.ID, percent.ID})
	assertDeviceInventory(t, repository, device.DeviceInventoryInput{Search: "_", Page: 1, PerPage: 20}, []uuid.UUID{underscore.ID})
	assertDeviceInventory(t, repository, device.DeviceInventoryInput{Search: `\`, Page: 1, PerPage: 20}, []uuid.UUID{backslash.ID})
	assertDeviceInventory(t, repository, device.DeviceInventoryInput{Search: `'; DROP TABLE devices; --`, Page: 1, PerPage: 20}, []uuid.UUID{})
	assertDeviceInventory(t, repository, device.DeviceInventoryInput{VGatewayID: &gatewayPercent, Enabled: boolPointer(true), Page: 1, PerPage: 20}, []uuid.UUID{alpha.ID, beta.ID})
	deviceType := device.DeviceTypeModbus
	assertDeviceInventory(t, repository, device.DeviceInventoryInput{Type: &deviceType, Page: 2, PerPage: 2}, []uuid.UUID{percent.ID, backslash.ID})

	result, err := repository.ListDeviceInventory(ctx, device.DeviceInventoryInput{Search: "alpha", Page: 1, PerPage: 20})
	if err != nil {
		t.Fatalf("ListDeviceInventory(alpha) error = %v", err)
	}
	if result.Total != 1 || len(result.Data) != 1 || result.Data[0].ID != alpha.ID || result.Data[0].VGatewayName != "Plant % North" || result.Data[0].VGatewayType != "modbus_tcp" || !result.Data[0].VGatewayEnabled || result.Data[0].DatasourceCount != 2 || result.Data[0].TagCount != 2 {
		t.Fatalf("ListDeviceInventory(alpha) = %#v", result)
	}

	empty, err := repository.ListDeviceInventory(ctx, device.DeviceInventoryInput{Search: "missing", Page: 1, PerPage: 20})
	if err != nil || empty.Total != 0 || empty.TotalPages != 0 || len(empty.Data) != 0 || empty.Data == nil {
		t.Fatalf("ListDeviceInventory(empty) = %#v, %v", empty, err)
	}
}

func TestRepositoryMapsReferencesMissingRowsAndCancellation_Integration(t *testing.T) {
	database := newDeviceRepositoryDatabase(t)
	repository := NewRepository(database)
	ctx := context.Background()
	gatewayID := insertDeviceGateway(t, database, "Cancellation", true)
	entity := insertDevice(t, repository, gatewayID, "Meter", true)
	datasource := insertDatasource(t, repository, entity.ID, "Source")
	missingID := uuid.New()

	if _, err := repository.FindGateway(ctx, missingID); !errors.Is(err, device.ErrGatewayNotFound) {
		t.Errorf("FindGateway(missing) error = %v", err)
	}
	if _, err := repository.FindDevice(ctx, missingID); !errors.Is(err, device.ErrDeviceNotFound) {
		t.Errorf("FindDevice(missing) error = %v", err)
	}
	if _, err := repository.FindDatasource(ctx, missingID); !errors.Is(err, device.ErrDatasourceNotFound) {
		t.Errorf("FindDatasource(missing) error = %v", err)
	}
	if err := repository.UpdateDevice(ctx, &device.Device{ID: missingID}); !errors.Is(err, device.ErrDeviceNotFound) {
		t.Errorf("UpdateDevice(missing) error = %v", err)
	}
	if err := repository.UpdateDatasource(ctx, &device.Datasource{ID: missingID}); !errors.Is(err, device.ErrDatasourceNotFound) {
		t.Errorf("UpdateDatasource(missing) error = %v", err)
	}
	if err := repository.DeleteDevice(ctx, missingID); !errors.Is(err, device.ErrDeviceNotFound) {
		t.Errorf("DeleteDevice(missing) error = %v", err)
	}
	if err := repository.DeleteDatasource(ctx, missingID); !errors.Is(err, device.ErrDatasourceNotFound) {
		t.Errorf("DeleteDatasource(missing) error = %v", err)
	}
	if err := repository.CreateDevice(ctx, &device.Device{VGatewayID: missingID, Name: "Orphan", Type: device.DeviceTypeModbus, Config: json.RawMessage(`{}`)}); !errors.Is(err, device.ErrGatewayNotFound) {
		t.Errorf("CreateDevice(missing gateway) error = %v", err)
	}
	if err := repository.CreateDatasource(ctx, &device.Datasource{DeviceID: missingID, Name: "Orphan", Type: device.DatasourceTypeModbusRead, Config: json.RawMessage(`{}`)}); !errors.Is(err, device.ErrDeviceNotFound) {
		t.Errorf("CreateDatasource(missing device) error = %v", err)
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	operations := map[string]func() error{
		"count gateways": func() error {
			_, err := repository.CountByVGatewayIDs(cancelled, []uuid.UUID{gatewayID})
			return err
		},
		"find gateway": func() error {
			_, err := repository.FindGateway(cancelled, gatewayID)
			return err
		},
		"create device": func() error {
			return repository.CreateDevice(cancelled, &device.Device{VGatewayID: gatewayID, Name: "Cancelled", Type: device.DeviceTypeModbus, Config: json.RawMessage(`{}`)})
		},
		"find device": func() error {
			_, err := repository.FindDevice(cancelled, entity.ID)
			return err
		},
		"list devices": func() error {
			_, err := repository.ListDevices(cancelled, gatewayID)
			return err
		},
		"inventory": func() error {
			_, err := repository.ListDeviceInventory(cancelled, device.DeviceInventoryInput{Page: 1, PerPage: 20})
			return err
		},
		"update device": func() error {
			copy := *entity
			copy.Name = "Cancelled"
			return repository.UpdateDevice(cancelled, &copy)
		},
		"delete device": func() error { return repository.DeleteDevice(cancelled, entity.ID) },
		"create datasource": func() error {
			return repository.CreateDatasource(cancelled, &device.Datasource{DeviceID: entity.ID, Name: "Cancelled", Type: device.DatasourceTypeModbusRead, Config: json.RawMessage(`{}`)})
		},
		"find datasource": func() error {
			_, err := repository.FindDatasource(cancelled, datasource.ID)
			return err
		},
		"list datasources": func() error {
			_, err := repository.ListDatasources(cancelled, entity.ID)
			return err
		},
		"update datasource": func() error {
			copy := *datasource
			copy.Name = "Cancelled"
			return repository.UpdateDatasource(cancelled, &copy)
		},
		"delete datasource": func() error { return repository.DeleteDatasource(cancelled, datasource.ID) },
	}
	for name, operation := range operations {
		t.Run(name, func(t *testing.T) {
			if err := operation(); !errors.Is(err, context.Canceled) {
				t.Errorf("error = %v, want context.Canceled", err)
			}
		})
	}
	reloaded, err := repository.FindDevice(ctx, entity.ID)
	if err != nil || reloaded.Name != "Meter" {
		t.Fatalf("Device changed after cancelled operations: %#v, %v", reloaded, err)
	}
	reloadedDatasource, err := repository.FindDatasource(ctx, datasource.ID)
	if err != nil || reloadedDatasource.Name != "Source" {
		t.Fatalf("Datasource changed after cancelled operations: %#v, %v", reloadedDatasource, err)
	}
}

func assertDeviceInventory(t *testing.T, repository *repository, input device.DeviceInventoryInput, want []uuid.UUID) {
	t.Helper()
	result, err := repository.ListDeviceInventory(context.Background(), input)
	if err != nil {
		t.Fatalf("ListDeviceInventory(%#v) error = %v", input, err)
	}
	if result.Total != int64(len(want)) && input.Page == 1 {
		t.Fatalf("ListDeviceInventory(%#v) total = %d, want %d", input, result.Total, len(want))
	}
	if len(result.Data) != len(want) {
		t.Fatalf("ListDeviceInventory(%#v) returned %d rows, want %d: %#v", input, len(result.Data), len(want), result.Data)
	}
	for index, id := range want {
		if result.Data[index].ID != id {
			t.Errorf("ListDeviceInventory(%#v)[%d] ID = %s, want %s", input, index, result.Data[index].ID, id)
		}
	}
	if input.Page == 2 && (result.Total != 5 || result.TotalPages != 3) {
		t.Errorf("ListDeviceInventory pagination = total %d, pages %d; want 5, 3", result.Total, result.TotalPages)
	}
}

func insertDeviceGateway(t *testing.T, database *gorm.DB, name string, enabled bool) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := database.Raw(`
		INSERT INTO vgateways (name,type,enabled,config)
		VALUES (?,'modbus_tcp',?,CAST(? AS jsonb)) RETURNING id
	`, name, enabled, `{"host":"127.0.0.1","port":502}`).Row().Scan(&id); err != nil {
		t.Fatalf("inserting vGateway %q: %v", name, err)
	}
	return id
}

func insertDevice(t *testing.T, repository *repository, gatewayID uuid.UUID, name string, enabled bool) *device.Device {
	t.Helper()
	entity := &device.Device{VGatewayID: gatewayID, Name: name, Type: device.DeviceTypeModbus, Enabled: enabled, Config: json.RawMessage(`{"unit_id":1}`)}
	if err := repository.CreateDevice(context.Background(), entity); err != nil {
		t.Fatalf("creating Device %q: %v", name, err)
	}
	return entity
}

func insertDatasource(t *testing.T, repository *repository, deviceID uuid.UUID, name string) *device.Datasource {
	t.Helper()
	entity := &device.Datasource{DeviceID: deviceID, Name: name, Type: device.DatasourceTypeModbusRead, Enabled: true, Config: json.RawMessage(`{"function_code":3,"start_address":0,"quantity":1}`)}
	if err := repository.CreateDatasource(context.Background(), entity); err != nil {
		t.Fatalf("creating Datasource %q: %v", name, err)
	}
	return entity
}

func insertReadingTag(t *testing.T, database *gorm.DB, datasourceID uuid.UUID, name string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := database.Raw(`
		INSERT INTO tags (datasource_id,name,type,data_type,enabled,config)
		VALUES (?,?,'reading','float64',true,CAST(? AS jsonb)) RETURNING id
	`, datasourceID, name, `{}`).Row().Scan(&id); err != nil {
		t.Fatalf("inserting Tag %q: %v", name, err)
	}
	return id
}

func assertDeviceRowCount(t *testing.T, database *gorm.DB, tableName, columnName string, id uuid.UUID, want int64) {
	t.Helper()
	var count int64
	if err := database.Table(tableName).Where(columnName+" = ?", id).Count(&count).Error; err != nil {
		t.Fatalf("counting %s: %v", tableName, err)
	}
	if count != want {
		t.Errorf("%s count = %d, want %d", tableName, count, want)
	}
}

func newDeviceRepositoryDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run Device repository integration tests")
	}
	adminDatabase, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("opening PostgreSQL: %v", err)
	}
	t.Cleanup(func() { _ = adminDatabase.Close() })
	schema := "device_repository_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := adminDatabase.Exec("CREATE SCHEMA " + schema); err != nil {
		t.Fatalf("creating schema: %v", err)
	}
	t.Cleanup(func() {
		if _, err := adminDatabase.Exec("DROP SCHEMA IF EXISTS " + schema + " CASCADE"); err != nil {
			t.Errorf("dropping schema: %v", err)
		}
	})
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatalf("parsing database URL: %v", err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	database, err := gorm.Open(gormpostgres.Open(parsed.String()), &gorm.Config{})
	if err != nil {
		t.Fatalf("opening GORM: %v", err)
	}
	sqlDatabase, err := database.DB()
	if err != nil {
		t.Fatalf("getting SQL DB: %v", err)
	}
	t.Cleanup(func() { _ = sqlDatabase.Close() })
	for _, migration := range []string{
		"../../../migrations/000002_create_vgateways.up.sql",
		"../../../migrations/000003_create_devices_datasources.up.sql",
		"../../../migrations/000004_create_tags.up.sql",
	} {
		contents, err := os.ReadFile(migration)
		if err != nil {
			t.Fatalf("reading migration %s: %v", migration, err)
		}
		if err := database.Exec(string(contents)).Error; err != nil {
			t.Fatalf("applying migration %s: %v", migration, err)
		}
	}
	return database
}

func boolPointer(value bool) *bool { return &value }

func stringPointer(value string) *string { return &value }
