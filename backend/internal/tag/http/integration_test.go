package taghttp

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/thefuriousowl/iot-edge/internal/protocol"
	"github.com/thefuriousowl/iot-edge/internal/tag"
	tagpostgres "github.com/thefuriousowl/iot-edge/internal/tag/postgres"
	gormpostgres "gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestTagCRUDPreviewAndValidation_EndToEnd(t *testing.T) {
	db, datasourceID := newTagIntegrationDatabase(t)
	source := &integrationDatasourceReader{samples: map[uuid.UUID]protocol.DatasourceSample{
		datasourceID: {Quality: "good", Raw: []byte{0x2A, 0x00}},
	}}
	service, err := tag.NewService(tagpostgres.NewRepository(db), source, tag.NewBinaryNumericDecoder())
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	app := fiber.New()
	RegisterRoutes(app.Group("/api"), NewHandler(service))
	t.Cleanup(func() { _ = app.Shutdown() })

	var constant tag.Tag
	response := performRequest(t, app, http.MethodPost, "/api/tags/", `{"name":"Base","type":"constant","data_type":"uint16","config":{"value":10}}`)
	assertStatus(t, response, fiber.StatusCreated)
	decodeResponse(t, response, &constant)
	if constant.ID == uuid.Nil || constant.Name != "Base" || string(constant.Config) != `{"value":10}` {
		t.Errorf("constant = %#v", constant)
	}

	var reading tag.Tag
	response = performRequest(t, app, http.MethodPost, "/api/tags/", `{"datasource_id":"`+datasourceID.String()+`","name":"Input","type":"reading","data_type":"uint16","config":{"decoder":{"type":"binary_numeric","config":{"byte_order":"little_endian"}}}}`)
	assertStatus(t, response, fiber.StatusCreated)
	decodeResponse(t, response, &reading)
	if reading.ID == uuid.Nil || reading.DatasourceID == nil || *reading.DatasourceID != datasourceID || string(reading.Config) != `{"decoder":{"type":"binary_numeric","config":{"byte_offset":0,"byte_order":"little_endian","bit_offset":0}}}` {
		t.Errorf("reading = %#v", reading)
	}

	expression := fmt.Sprintf("${%s} + ${%s}", reading.ID, constant.ID)
	response = performRequest(t, app, http.MethodPost, "/api/tags/validate-expression", fmt.Sprintf(`{"expression":%q}`, expression))
	assertStatus(t, response, fiber.StatusOK)
	var validated struct {
		Valid        bool        `json:"valid"`
		Dependencies []uuid.UUID `json:"dependencies"`
	}
	decodeResponse(t, response, &validated)
	if !validated.Valid || len(validated.Dependencies) != 2 || validated.Dependencies[0] != reading.ID || validated.Dependencies[1] != constant.ID {
		t.Errorf("validation = %#v", validated)
	}

	var calculated tag.Tag
	response = performRequest(t, app, http.MethodPost, "/api/tags/", fmt.Sprintf(`{"name":"Sum","type":"calculated","data_type":"uint16","description":"Input plus base","config":{"expression":%q,"trigger":{"tag_id":"%s","mode":"on_sample"}}}`, expression, reading.ID))
	assertStatus(t, response, fiber.StatusCreated)
	decodeResponse(t, response, &calculated)
	if calculated.ID == uuid.Nil || calculated.Description == nil || *calculated.Description != "Input plus base" {
		t.Errorf("calculated = %#v", calculated)
	}

	response = performRequest(t, app, http.MethodPost, "/api/tags/"+calculated.ID.String()+"/preview", "")
	assertAPIError(t, response, fiber.StatusConflict, "TAG008")
	if source.calls[datasourceID] != 0 {
		t.Errorf("calculated preview datasource calls = %d, want 0", source.calls[datasourceID])
	}

	response = performRequest(t, app, http.MethodPost, "/api/tags/preview", `{"type":"constant","data_type":"bool","config":{"value":true}}`)
	assertStatus(t, response, fiber.StatusOK)
	var unsaved tag.PreviewResult
	decodeResponse(t, response, &unsaved)
	if unsaved.TagID != uuid.Nil || unsaved.DataType != tag.DataTypeBool || unsaved.Value != true {
		t.Errorf("unsaved preview = %#v", unsaved)
	}

	query := url.Values{"type": {"calculated"}, "data_type": {"uint16"}, "enabled": {"true"}, "search": {"sum"}, "page": {"1"}, "per_page": {"1"}}
	response = performRequest(t, app, http.MethodGet, "/api/tags/?"+query.Encode(), "")
	assertStatus(t, response, fiber.StatusOK)
	var list struct {
		Data       []tag.Tag `json:"data"`
		Pagination struct {
			Total      int64 `json:"total"`
			TotalPages int   `json:"total_pages"`
		} `json:"pagination"`
	}
	decodeResponse(t, response, &list)
	if len(list.Data) != 1 || list.Data[0].ID != calculated.ID || list.Pagination.Total != 1 || list.Pagination.TotalPages != 1 {
		t.Errorf("list = %#v", list)
	}

	response = performRequest(t, app, http.MethodPut, "/api/tags/"+constant.ID.String(), `{"description":"Updated base","config":{"value":12}}`)
	assertStatus(t, response, fiber.StatusOK)
	decodeResponse(t, response, &constant)
	if constant.Description == nil || *constant.Description != "Updated base" || string(constant.Config) != `{"value":12}` {
		t.Errorf("updated constant = %#v", constant)
	}

	response = performRequest(t, app, http.MethodPost, "/api/tags/"+calculated.ID.String()+"/preview", "")
	assertAPIError(t, response, fiber.StatusConflict, "TAG008")

	response = performRequest(t, app, http.MethodPost, "/api/tags/", `{"name":"Base","type":"constant","data_type":"uint16","config":{"value":1}}`)
	assertAPIError(t, response, fiber.StatusConflict, "TAG001")

	response = performRequest(t, app, http.MethodPut, "/api/tags/"+calculated.ID.String(), fmt.Sprintf(`{"config":{"expression":"${%s}","trigger":{"tag_id":"%s","mode":"on_sample"}}}`, calculated.ID, reading.ID))
	assertAPIError(t, response, fiber.StatusBadRequest, "VALIDATION_ERROR")

	response = performRequest(t, app, http.MethodPut, "/api/tags/"+calculated.ID.String(), `{"enabled":false}`)
	assertStatus(t, response, fiber.StatusOK)
	closeResponse(t, response)
	response = performRequest(t, app, http.MethodPost, "/api/tags/"+calculated.ID.String()+"/preview", "")
	assertAPIError(t, response, fiber.StatusConflict, "TAG006")

	response = performRequest(t, app, http.MethodDelete, "/api/tags/"+constant.ID.String(), "")
	assertStatus(t, response, fiber.StatusNoContent)
	closeResponse(t, response)
	response = performRequest(t, app, http.MethodGet, "/api/tags/"+constant.ID.String(), "")
	assertAPIError(t, response, fiber.StatusNotFound, "TAG004")
}

type integrationDatasourceReader struct {
	samples map[uuid.UUID]protocol.DatasourceSample
	calls   map[uuid.UUID]int
	err     error
}

func (reader *integrationDatasourceReader) ReadDatasourceForTag(_ context.Context, id uuid.UUID) (protocol.DatasourceSample, error) {
	if reader.calls == nil {
		reader.calls = map[uuid.UUID]int{}
	}
	reader.calls[id]++
	if reader.err != nil {
		return protocol.DatasourceSample{}, reader.err
	}
	sample, exists := reader.samples[id]
	if !exists {
		return protocol.DatasourceSample{}, errors.New("sample not found")
	}
	return sample, nil
}

func newTagIntegrationDatabase(t *testing.T) (*gorm.DB, uuid.UUID) {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run Tag HTTP integration tests")
	}
	adminDB, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("opening PostgreSQL: %v", err)
	}
	t.Cleanup(func() { _ = adminDB.Close() })
	schema := "tag_http_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
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
	if err := db.Exec(`INSERT INTO vgateways (id,name,type,config) VALUES (?,?,?,?)`, gatewayID, "Tag Gateway", "modbus_tcp", `{}`).Error; err != nil {
		t.Fatalf("inserting gateway: %v", err)
	}
	if err := db.Exec(`INSERT INTO devices (id,vgateway_id,name,type,config) VALUES (?,?,?,?,?)`, deviceID, gatewayID, "Tag Device", "modbus_device", `{}`).Error; err != nil {
		t.Fatalf("inserting device: %v", err)
	}
	if err := db.Exec(`INSERT INTO datasources (id,device_id,name,type,config) VALUES (?,?,?,?,?)`, datasourceID, deviceID, "Tag Source", "modbus_read", `{}`).Error; err != nil {
		t.Fatalf("inserting datasource: %v", err)
	}
	return db, datasourceID
}
