package tagpostgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/thefuriousowl/iot-edge/internal/tag"
	gormpostgres "gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestRepositoryCRUDDependenciesAndFilters_Integration(t *testing.T) {
	db, datasourceID := newRepositoryDatabase(t)
	repository := NewRepository(db)
	ctx := context.Background()

	reading := tag.Tag{DatasourceID: &datasourceID, Name: "Line voltage", Type: tag.TypeReading, DataType: tag.DataTypeUInt16, Enabled: true, Config: json.RawMessage(`{"decoder":{"type":"binary_numeric"}}`)}
	if err := repository.Create(ctx, &reading, nil); err != nil {
		t.Fatalf("Create(reading) error = %v", err)
	}
	constant := tag.Tag{Name: "Nominal voltage", Type: tag.TypeConstant, DataType: tag.DataTypeFloat64, Enabled: false, Config: json.RawMessage(`{"value":230}`)}
	if err := repository.Create(ctx, &constant, nil); err != nil {
		t.Fatalf("Create(constant) error = %v", err)
	}
	calculated := tag.Tag{Name: "Voltage delta", Type: tag.TypeCalculated, DataType: tag.DataTypeFloat64, Enabled: true, Config: json.RawMessage(`{"expression":"reading - constant"}`)}
	if err := repository.Create(ctx, &calculated, []uuid.UUID{reading.ID, constant.ID}); err != nil {
		t.Fatalf("Create(calculated) error = %v", err)
	}

	found, err := repository.Find(ctx, reading.ID)
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	var storedConfig struct {
		Decoder struct {
			Type string `json:"type"`
		} `json:"decoder"`
	}
	if err := json.Unmarshal(found.Config, &storedConfig); err != nil {
		t.Fatalf("decoding stored config: %v", err)
	}
	if found.ID != reading.ID || found.DatasourceID == nil || *found.DatasourceID != datasourceID || storedConfig.Decoder.Type != "binary_numeric" {
		t.Errorf("Find() = %#v", found)
	}

	assertTagList(t, repository, tag.ListInput{Type: typePointer(tag.TypeConstant)}, 1, constant.ID)
	assertTagList(t, repository, tag.ListInput{DataType: dataTypePointer(tag.DataTypeUInt16)}, 1, reading.ID)
	assertTagList(t, repository, tag.ListInput{Enabled: boolPointer(false)}, 1, constant.ID)
	assertTagList(t, repository, tag.ListInput{DatasourceID: &datasourceID}, 1, reading.ID)
	assertTagList(t, repository, tag.ListInput{Search: " LINE "}, 1, reading.ID)
	assertTagList(t, repository, tag.ListInput{Search: "%"}, 0, uuid.Nil)
	page, err := repository.List(ctx, tag.ListInput{Page: 1, PerPage: 2})
	if err != nil {
		t.Fatalf("List(paginated) error = %v", err)
	}
	if len(page.Data) != 2 || page.Total != 3 || page.Page != 1 || page.PerPage != 2 || page.TotalPages != 2 {
		t.Errorf("List(paginated) = %#v", page)
	}

	calculated.Name = "Voltage difference"
	calculated.Type = tag.TypeConstant
	calculated.DataType = tag.DataTypeFloat32
	calculated.Enabled = false
	calculated.Config = json.RawMessage(`{"expression":"reading"}`)
	if err := repository.Update(ctx, &calculated, []uuid.UUID{reading.ID}); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	updated, err := repository.Find(ctx, calculated.ID)
	if err != nil {
		t.Fatalf("Find(updated) error = %v", err)
	}
	if updated.Name != "Voltage difference" || updated.Type != tag.TypeCalculated || updated.DataType != tag.DataTypeFloat32 || updated.Enabled {
		t.Errorf("updated tag = %#v", updated)
	}
	dependencies, err := repository.ListDependencies(ctx)
	if err != nil {
		t.Fatalf("ListDependencies() error = %v", err)
	}
	if len(dependencies) != 1 || dependencies[0].TagID != calculated.ID || dependencies[0].DependsOnTagID != reading.ID {
		t.Errorf("dependencies = %#v", dependencies)
	}

	calculated.Name = "Must roll back"
	if err := repository.Update(ctx, &calculated, []uuid.UUID{uuid.New()}); !errors.Is(err, tag.ErrDependencyMissing) {
		t.Fatalf("Update(missing dependency) error = %v", err)
	}
	rolledBack, err := repository.Find(ctx, calculated.ID)
	if err != nil {
		t.Fatalf("Find(rolled back) error = %v", err)
	}
	if rolledBack.Name != "Voltage difference" {
		t.Errorf("rolled back name = %q, want Voltage difference", rolledBack.Name)
	}
	dependencies, err = repository.ListDependencies(ctx)
	if err != nil || len(dependencies) != 1 || dependencies[0].DependsOnTagID != reading.ID {
		t.Errorf("dependencies after rollback = %#v, error = %v", dependencies, err)
	}

	duplicate := tag.Tag{Name: reading.Name, Type: tag.TypeConstant, DataType: tag.DataTypeFloat64, Enabled: true, Config: json.RawMessage(`{}`)}
	if err := repository.Create(ctx, &duplicate, nil); !errors.Is(err, tag.ErrTagNameExists) {
		t.Fatalf("Create(duplicate) error = %v", err)
	}
	missingSourceID := uuid.New()
	missingSource := tag.Tag{DatasourceID: &missingSourceID, Name: "Missing source", Type: tag.TypeReading, DataType: tag.DataTypeUInt16, Enabled: true, Config: json.RawMessage(`{}`)}
	if err := repository.Create(ctx, &missingSource, nil); !errors.Is(err, tag.ErrDatasourceMissing) {
		t.Fatalf("Create(missing source) error = %v", err)
	}
	invalidScope := tag.Tag{DatasourceID: &datasourceID, Name: "Invalid scope", Type: tag.TypeConstant, DataType: tag.DataTypeFloat64, Enabled: true, Config: json.RawMessage(`{}`)}
	if err := repository.Create(ctx, &invalidScope, nil); !errors.Is(err, tag.ErrInvalidTag) {
		t.Fatalf("Create(invalid scope) error = %v", err)
	}
	duplicateDependency := tag.Tag{Name: "Duplicate dependency", Type: tag.TypeCalculated, DataType: tag.DataTypeFloat64, Enabled: true, Config: json.RawMessage(`{}`)}
	if err := repository.Create(ctx, &duplicateDependency, []uuid.UUID{reading.ID, reading.ID}); !errors.Is(err, tag.ErrInvalidDependency) {
		t.Fatalf("Create(duplicate dependency) error = %v", err)
	}
	selfDependency := tag.Tag{ID: uuid.New(), Name: "Self dependency", Type: tag.TypeCalculated, DataType: tag.DataTypeFloat64, Enabled: true, Config: json.RawMessage(`{}`)}
	if err := repository.Create(ctx, &selfDependency, []uuid.UUID{selfDependency.ID}); !errors.Is(err, tag.ErrInvalidDependency) {
		t.Fatalf("Create(self dependency) error = %v", err)
	}
	rolledBackCreate := tag.Tag{Name: "Missing dependency", Type: tag.TypeCalculated, DataType: tag.DataTypeFloat64, Enabled: true, Config: json.RawMessage(`{}`)}
	if err := repository.Create(ctx, &rolledBackCreate, []uuid.UUID{uuid.New()}); !errors.Is(err, tag.ErrDependencyMissing) {
		t.Fatalf("Create(missing dependency) error = %v", err)
	}
	if _, err := repository.Find(ctx, rolledBackCreate.ID); !errors.Is(err, tag.ErrTagNotFound) {
		t.Fatalf("Find(rolled back create) error = %v", err)
	}

	if err := repository.Delete(ctx, calculated.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	dependencies, err = repository.ListDependencies(ctx)
	if err != nil || len(dependencies) != 0 {
		t.Errorf("dependencies after delete = %#v, error = %v", dependencies, err)
	}
	if _, err := repository.Find(ctx, calculated.ID); !errors.Is(err, tag.ErrTagNotFound) {
		t.Fatalf("Find(deleted) error = %v", err)
	}
	if err := repository.Delete(ctx, calculated.ID); !errors.Is(err, tag.ErrTagNotFound) {
		t.Fatalf("Delete(missing) error = %v", err)
	}
}

func assertTagList(t *testing.T, repository tag.Repository, input tag.ListInput, wantCount int, wantID uuid.UUID) {
	t.Helper()
	result, err := repository.List(context.Background(), input)
	if err != nil {
		t.Fatalf("List(%#v) error = %v", input, err)
	}
	if len(result.Data) != wantCount || result.Total != int64(wantCount) {
		t.Fatalf("List(%#v) = %#v", input, result)
	}
	if wantCount == 1 && result.Data[0].ID != wantID {
		t.Errorf("List(%#v) ID = %s, want %s", input, result.Data[0].ID, wantID)
	}
}

func newRepositoryDatabase(t *testing.T) (*gorm.DB, uuid.UUID) {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run tag repository integration tests")
	}
	adminDB, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("opening PostgreSQL: %v", err)
	}
	t.Cleanup(func() { _ = adminDB.Close() })
	schema := "tag_repository_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := adminDB.Exec("CREATE SCHEMA " + schema); err != nil {
		t.Fatalf("creating schema: %v", err)
	}
	t.Cleanup(func() {
		if _, err := adminDB.Exec("DROP SCHEMA IF EXISTS " + schema + " CASCADE"); err != nil {
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
	db, err := gorm.Open(gormpostgres.Open(parsed.String()), &gorm.Config{})
	if err != nil {
		t.Fatalf("opening GORM: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("getting SQL DB: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	for _, migrationPath := range []string{"../../../migrations/000002_create_vgateways.up.sql", "../../../migrations/000003_create_devices_datasources.up.sql", "../../../migrations/000004_create_tags.up.sql"} {
		migration, err := os.ReadFile(migrationPath)
		if err != nil {
			t.Fatalf("reading migration: %v", err)
		}
		if err := db.Exec(string(migration)).Error; err != nil {
			t.Fatalf("applying migration: %v", err)
		}
	}
	gatewayID := uuid.New()
	deviceID := uuid.New()
	datasourceID := uuid.New()
	if err := db.Exec(`INSERT INTO vgateways (id,name,type,config) VALUES (?,?,?,?)`, gatewayID, "Tag repository gateway", "modbus_tcp", `{}`).Error; err != nil {
		t.Fatalf("inserting gateway: %v", err)
	}
	if err := db.Exec(`INSERT INTO devices (id,vgateway_id,name,type,config) VALUES (?,?,?,?,?)`, deviceID, gatewayID, "Tag repository device", "modbus_device", `{}`).Error; err != nil {
		t.Fatalf("inserting device: %v", err)
	}
	if err := db.Exec(`INSERT INTO datasources (id,device_id,name,type,config) VALUES (?,?,?,?,?)`, datasourceID, deviceID, "Tag repository datasource", "modbus_read", `{}`).Error; err != nil {
		t.Fatalf("inserting datasource: %v", err)
	}
	return db, datasourceID
}

func typePointer(value tag.Type) *tag.Type             { return &value }
func dataTypePointer(value tag.DataType) *tag.DataType { return &value }
func boolPointer(value bool) *bool                     { return &value }
