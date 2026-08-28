package assethttp

import (
	"context"
	"database/sql"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/thefuriousowl/iot-edge/internal/asset"
	assetconnectivity "github.com/thefuriousowl/iot-edge/internal/asset/connectivity"
	assetpostgres "github.com/thefuriousowl/iot-edge/internal/asset/postgres"
	"github.com/thefuriousowl/iot-edge/internal/device"
	"github.com/thefuriousowl/iot-edge/internal/tag"
	"github.com/thefuriousowl/iot-edge/internal/vgateway"
	"github.com/thefuriousowl/iot-edge/migrations"
	gormpostgres "gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type integrationCatalog struct {
	descriptors map[string]asset.SourceDescriptor
}

func (catalog *integrationCatalog) Describe(_ context.Context, reference asset.SourceReference) (asset.SourceDescriptor, error) {
	descriptor, exists := catalog.descriptors[reference.Key()]
	if !exists {
		return asset.SourceDescriptor{}, asset.ErrBindingSourceMissing
	}
	return descriptor, nil
}

type integrationLiveReader struct {
	samples map[asset.SourceReference]asset.LiveSample
	history map[asset.SourceReference][]asset.LiveSample
}

type integrationTagReader struct{ values map[uuid.UUID]tag.Tag }

func (reader *integrationTagReader) Get(_ context.Context, id uuid.UUID) (*tag.Tag, error) {
	value, exists := reader.values[id]
	if !exists {
		return nil, tag.ErrTagNotFound
	}
	return &value, nil
}

type integrationDeviceReader struct{}

func (*integrationDeviceReader) GetDatasource(context.Context, uuid.UUID) (*device.DatasourceView, error) {
	return nil, device.ErrDatasourceNotFound
}
func (*integrationDeviceReader) GetDevice(context.Context, uuid.UUID) (*device.DeviceView, error) {
	return nil, device.ErrDeviceNotFound
}

type integrationGatewayReader struct{}

func (*integrationGatewayReader) Get(context.Context, uuid.UUID) (*vgateway.VGatewayView, error) {
	return nil, vgateway.ErrVGatewayNotFound
}

func (reader *integrationLiveReader) Snapshot(_ context.Context, references []asset.SourceReference) (asset.LiveSnapshot, error) {
	values := make([]asset.LiveSample, 0, len(references))
	for _, reference := range references {
		if value, exists := reader.samples[reference]; exists {
			values = append(values, value)
		}
	}
	return asset.LiveSnapshot{CapturedAt: time.Now().UTC(), Samples: values}, nil
}
func (reader *integrationLiveReader) History(reference asset.SourceReference, limit int) []asset.LiveSample {
	values := reader.history[reference]
	if len(values) > limit {
		values = values[len(values)-limit:]
	}
	return append([]asset.LiveSample(nil), values...)
}
func (*integrationLiveReader) Subscribe(context.Context, []asset.SourceReference) (asset.LiveSubscription, error) {
	return nil, asset.ErrMeasurementUnavailable
}

func TestAssetProtectedAPI_EndToEnd(t *testing.T) {
	database := newIntegrationDatabase(t)
	catalog := &integrationCatalog{descriptors: map[string]asset.SourceDescriptor{}}
	repository := assetpostgres.NewRepository(database)
	service, err := asset.NewService(repository, catalog)
	if err != nil {
		t.Fatal(err)
	}
	liveReader := &integrationLiveReader{samples: map[asset.SourceReference]asset.LiveSample{}, history: map[asset.SourceReference][]asset.LiveSample{}}
	measurements, err := asset.NewMeasurementProjector(repository, liveReader)
	if err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	handler := NewHandler(service, WithMeasurements(measurements))
	RegisterRoutes(app.Group("/api"), handler)
	t.Cleanup(func() { _ = app.Shutdown() })

	response := request(t, app, http.MethodPost, "/api/assets/", `{"name":"Plant","kind":"site","timezone":"Asia/Bangkok","metadata":{"code":"P1"}}`)
	assertStatus(t, response, fiber.StatusCreated)
	var site asset.Asset
	decodeResponse(t, response, &site)
	if site.ID == uuid.Nil || site.Kind != asset.KindSite || string(site.Metadata) != `{"code":"P1"}` {
		t.Fatalf("site = %#v", site)
	}

	response = request(t, app, http.MethodPost, "/api/assets/", `{"parent_id":"`+site.ID.String()+`","name":"Main meter","kind":"meter"}`)
	assertStatus(t, response, fiber.StatusCreated)
	var meter asset.Asset
	decodeResponse(t, response, &meter)
	if meter.ParentID == nil || *meter.ParentID != site.ID {
		t.Fatalf("meter = %#v", meter)
	}
	response = request(t, app, http.MethodPost, "/api/assets/", `{"name":"Expansion plant","kind":"site","timezone":"Asia/Bangkok"}`)
	assertStatus(t, response, fiber.StatusCreated)
	var destination asset.Asset
	decodeResponse(t, response, &destination)
	response = request(t, app, http.MethodPost, "/api/assets/"+meter.ID.String()+"/move", `{"parent_id":"`+destination.ID.String()+`","position":4}`)
	assertStatus(t, response, fiber.StatusOK)
	decodeResponse(t, response, &meter)
	if meter.ParentID == nil || *meter.ParentID != destination.ID || meter.Position != 4 {
		t.Fatalf("moved meter = %#v", meter)
	}

	tagID := insertIntegrationTag(t, database)
	connectivity, err := assetconnectivity.NewResolver(repository, &integrationTagReader{values: map[uuid.UUID]tag.Tag{tagID: {ID: tagID, Name: "Asset API tag", Type: tag.TypeConstant, DataType: tag.DataTypeFloat64, Enabled: true}}}, &integrationDeviceReader{}, &integrationGatewayReader{})
	if err != nil {
		t.Fatal(err)
	}
	handler.connectivity = connectivity
	source := asset.TagSource(tagID)
	catalog.descriptors[source.Key()] = asset.SourceDescriptor{Reference: source, DataType: "float64", Unit: asset.UnitKilowattHour}
	bindingID := uuid.New()
	payload := `{"bindings":[{"id":"` + bindingID.String() + `","boundary_asset_id":"` + destination.ID.String() + `","source":{"kind":"tag","tag_id":"` + tagID.String() + `"},"semantic":{"resource":"electricity","quantity":"energy","unit":"kWh","precision":3},"meter_role":"main","rollup_policy":"include"}]}`
	response = request(t, app, http.MethodPut, "/api/assets/"+meter.ID.String()+"/bindings", payload)
	assertStatus(t, response, fiber.StatusOK)
	var bindings struct {
		Data []asset.MeasurementBinding `json:"data"`
	}
	decodeResponse(t, response, &bindings)
	if len(bindings.Data) != 1 || bindings.Data[0].OwnerAssetID != meter.ID || bindings.Data[0].SourceKey != source.Key() {
		t.Fatalf("bindings = %#v", bindings)
	}
	observedAt := time.Date(2026, time.August, 28, 12, 30, 0, 0, time.UTC)
	live := asset.LiveSample{Source: source, Available: true, Sequence: 21, Value: 42.5, Quality: "good", ObservedAt: &observedAt}
	liveReader.samples[source] = live
	liveReader.history[source] = []asset.LiveSample{live}
	response = request(t, app, http.MethodGet, "/api/assets/"+meter.ID.String()+"/measurements", "")
	assertStatus(t, response, fiber.StatusOK)
	var projected asset.MeasurementSnapshot
	decodeResponse(t, response, &projected)
	if len(projected.Measurements) != 1 || projected.Measurements[0].Latest.BindingID != bindingID || projected.Measurements[0].Latest.Sequence != 21 || projected.Measurements[0].Latest.Value != 42.5 || len(projected.Measurements[0].History) != 1 {
		t.Fatalf("measurement projection = %#v", projected)
	}
	response = request(t, app, http.MethodGet, "/api/assets/"+meter.ID.String()+"/connectivity", "")
	assertStatus(t, response, fiber.StatusOK)
	var assetLinks asset.AssetConnectivity
	decodeResponse(t, response, &assetLinks)
	if len(assetLinks.Links) != 1 || assetLinks.Links[0].Tag == nil || assetLinks.Links[0].Tag.ID != tagID || assetLinks.Links[0].Datasource != nil {
		t.Fatalf("Asset connectivity = %#v", assetLinks)
	}
	response = request(t, app, http.MethodGet, "/api/tags/"+tagID.String()+"/assets", "")
	assertStatus(t, response, fiber.StatusOK)
	var tagLinks asset.TagAssetConnectivity
	decodeResponse(t, response, &tagLinks)
	if len(tagLinks.Assets) != 1 || tagLinks.Assets[0].Asset.ID != meter.ID || tagLinks.Assets[0].BindingID != bindingID {
		t.Fatalf("Tag Asset connectivity = %#v", tagLinks)
	}

	response = request(t, app, http.MethodGet, "/api/assets/"+destination.ID.String()+"/tree", "")
	assertStatus(t, response, fiber.StatusOK)
	var tree asset.TreeNode
	decodeResponse(t, response, &tree)
	if len(tree.Children) != 1 || tree.Children[0].MeasurementCount != 1 {
		t.Fatalf("tree = %#v", tree)
	}

	response = request(t, app, http.MethodGet, "/api/assets/?search=meter&page=1&per_page=10", "")
	assertStatus(t, response, fiber.StatusOK)
	var list struct {
		Data []asset.Asset `json:"data"`
	}
	decodeResponse(t, response, &list)
	if len(list.Data) != 1 || list.Data[0].ID != meter.ID {
		t.Fatalf("list = %#v", list)
	}

	// Recompose every Asset-facing layer against the same database to model a
	// process restart and prove hierarchy/binding persistence is not in memory.
	restartedRepository := assetpostgres.NewRepository(database)
	restartedService, err := asset.NewService(restartedRepository, catalog)
	if err != nil {
		t.Fatal(err)
	}
	restartedMeasurements, err := asset.NewMeasurementProjector(restartedRepository, liveReader)
	if err != nil {
		t.Fatal(err)
	}
	restartedConnectivity, err := assetconnectivity.NewResolver(restartedRepository, &integrationTagReader{values: map[uuid.UUID]tag.Tag{tagID: {ID: tagID, Name: "Asset API tag", Type: tag.TypeConstant, DataType: tag.DataTypeFloat64, Enabled: true}}}, &integrationDeviceReader{}, &integrationGatewayReader{})
	if err != nil {
		t.Fatal(err)
	}
	restartedApp := fiber.New()
	RegisterRoutes(restartedApp.Group("/api"), NewHandler(restartedService, WithMeasurements(restartedMeasurements), WithConnectivity(restartedConnectivity)))
	t.Cleanup(func() { _ = restartedApp.Shutdown() })
	response = request(t, restartedApp, http.MethodGet, "/api/assets/"+meter.ID.String(), "")
	assertStatus(t, response, fiber.StatusOK)
	var persisted asset.Asset
	decodeResponse(t, response, &persisted)
	if persisted.ParentID == nil || *persisted.ParentID != destination.ID || persisted.Position != 4 {
		t.Fatalf("persisted meter after restart = %#v", persisted)
	}
	response = request(t, restartedApp, http.MethodGet, "/api/assets/"+meter.ID.String()+"/bindings", "")
	assertStatus(t, response, fiber.StatusOK)
	decodeResponse(t, response, &bindings)
	if len(bindings.Data) != 1 || bindings.Data[0].ID != bindingID {
		t.Fatalf("persisted bindings after restart = %#v", bindings)
	}

	response = request(t, app, http.MethodDelete, "/api/assets/"+meter.ID.String(), "")
	assertAPIError(t, response, fiber.StatusConflict, "ASSET_DEPENDENTS")
	response = request(t, app, http.MethodPut, "/api/assets/"+meter.ID.String()+"/bindings", `{"bindings":[]}`)
	assertStatus(t, response, fiber.StatusOK)
	closeBody(t, response)
	response = request(t, app, http.MethodDelete, "/api/assets/"+meter.ID.String(), "")
	assertStatus(t, response, fiber.StatusNoContent)
	closeBody(t, response)
	response = request(t, app, http.MethodDelete, "/api/assets/"+site.ID.String(), "")
	assertStatus(t, response, fiber.StatusNoContent)
	closeBody(t, response)
	response = request(t, app, http.MethodDelete, "/api/assets/"+destination.ID.String(), "")
	assertStatus(t, response, fiber.StatusNoContent)
	closeBody(t, response)
}

func newIntegrationDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run Asset HTTP integration tests")
	}
	admin, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	schema := "asset_http_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.Exec("CREATE SCHEMA " + schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.Exec("DROP SCHEMA IF EXISTS " + schema + " CASCADE") })
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", schema+",public")
	parsed.RawQuery = query.Encode()
	database, err := gorm.Open(gormpostgres.Open(parsed.String()), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := database.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := migrations.Up(context.Background(), sqlDB); err != nil {
		t.Fatalf("applying migrations: %v", err)
	}
	return database
}

func insertIntegrationTag(t *testing.T, database *gorm.DB) uuid.UUID {
	t.Helper()
	var rawID string
	if err := database.Raw(`INSERT INTO tags(name,type,data_type,config) VALUES ('Asset API tag','constant','float64','{"value":1}') RETURNING id`).Scan(&rawID).Error; err != nil {
		t.Fatal(err)
	}
	id, err := uuid.Parse(rawID)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
