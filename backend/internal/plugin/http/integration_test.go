package pluginhttp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/thefuriousowl/iot-edge/internal/plugin"
	pluginpostgres "github.com/thefuriousowl/iot-edge/internal/plugin/postgres"
	gormpostgres "gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestPluginLifecycleEndToEnd(t *testing.T) {
	database := newPluginHTTPIntegrationDatabase(t)
	definition := &integrationDefinition{}
	registry, err := plugin.NewRegistry(definition)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	repository := pluginpostgres.NewRepository(database)
	service, err := plugin.NewService(repository, registry)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	manager, err := plugin.NewManager(repository, registry, nil, plugin.WithManagerReconcileInterval(0))
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	if err := manager.Start(t.Context()); err != nil {
		t.Fatalf("Manager.Start() error = %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := manager.Stop(ctx); err != nil {
			t.Errorf("Manager.Stop() error = %v", err)
		}
	})
	app := newHandlerApp(service, manager)
	t.Cleanup(func() { _ = app.Shutdown() })

	response := request(t, app, http.MethodGet, "/api/plugin-types", "")
	assertStatus(t, response, fiber.StatusOK)
	var typesBody struct {
		Data []plugin.Manifest `json:"data"`
	}
	decodeResponse(t, response, &typesBody)
	if len(typesBody.Data) != 1 || typesBody.Data[0].Type != integrationPluginType || typesBody.Data[0].ConfigVersion != 1 {
		t.Errorf("types response = %#v", typesBody)
	}

	response = request(t, app, http.MethodPost, "/api/plugins/", `{"type":"energy_management","name":"Plant Energy","config":{"logger_id":"logger-a"}}`)
	assertStatus(t, response, fiber.StatusCreated)
	var created plugin.Instance
	decodeResponse(t, response, &created)
	if created.ID == uuid.Nil || created.Enabled || created.ConfigVersion != 1 || string(created.Config) != `{"logger_id":"logger-a"}` {
		t.Errorf("created = %#v", created)
	}
	if definition.runCount.Load() != 0 {
		t.Errorf("disabled instance run count = %d, want 0", definition.runCount.Load())
	}

	response = request(t, app, http.MethodGet, "/api/plugins/?type=energy_management&enabled=false&search=plant&page=1&per_page=10", "")
	assertStatus(t, response, fiber.StatusOK)
	var listed struct {
		Data       []map[string]any `json:"data"`
		Pagination struct {
			Total int64 `json:"total"`
		} `json:"pagination"`
	}
	decodeResponse(t, response, &listed)
	if len(listed.Data) != 1 || listed.Pagination.Total != 1 {
		t.Fatalf("list response = %#v", listed)
	}
	if _, exposed := listed.Data[0]["config"]; exposed {
		t.Error("list response exposes Plugin config")
	}

	response = request(t, app, http.MethodPut, "/api/plugins/"+created.ID.String(), `{"name":"Updated Energy","enabled":true,"config":{"logger_id":"logger-b"}}`)
	assertStatus(t, response, fiber.StatusOK)
	var updated plugin.Instance
	decodeResponse(t, response, &updated)
	if !updated.Enabled || updated.Name != "Updated Energy" || string(updated.Config) != `{"logger_id":"logger-b"}` {
		t.Errorf("updated = %#v", updated)
	}
	waitForRunCount(t, &definition.runCount, 1)

	response = request(t, app, http.MethodGet, "/api/plugins/"+created.ID.String()+"/status", "")
	assertStatus(t, response, fiber.StatusOK)
	var status struct {
		Enabled bool `json:"enabled"`
		Runtime struct {
			State     plugin.RuntimeState   `json:"state"`
			StartedAt *time.Time            `json:"started_at"`
			Error     *runtimeErrorResponse `json:"error"`
		} `json:"runtime"`
	}
	decodeResponse(t, response, &status)
	if !status.Enabled || status.Runtime.State != plugin.RuntimeStateRunning || status.Runtime.StartedAt == nil || status.Runtime.Error != nil {
		t.Errorf("status response = %#v", status)
	}

	response = request(t, app, http.MethodPost, "/api/plugins/"+created.ID.String()+"/restart", `{}`)
	assertStatus(t, response, fiber.StatusOK)
	closeBody(t, response)
	waitForRunCount(t, &definition.runCount, 2)

	response = request(t, app, http.MethodPost, "/api/plugins/", `{"type":"energy_management","name":"Second Energy","config":{"logger_id":"logger-c"}}`)
	assertError(t, response, fiber.StatusConflict, "PLG004")

	response = request(t, app, http.MethodPost, "/api/plugins/"+created.ID.String()+"/disable", "")
	assertStatus(t, response, fiber.StatusOK)
	closeBody(t, response)
	if state := manager.Status(created.ID).State; state != plugin.RuntimeStateStopped {
		t.Errorf("runtime state after disable = %q, want stopped", state)
	}

	response = request(t, app, http.MethodDelete, "/api/plugins/"+created.ID.String(), "")
	assertStatus(t, response, fiber.StatusNoContent)
	closeBody(t, response)
	response = request(t, app, http.MethodGet, "/api/plugins/"+created.ID.String(), "")
	assertError(t, response, fiber.StatusNotFound, "PLG001")
}

const integrationPluginType plugin.Type = "energy_management"

type integrationDefinition struct{ runCount atomic.Int32 }

func (*integrationDefinition) Manifest() plugin.Manifest {
	return plugin.Manifest{
		Type: integrationPluginType, Name: "Energy Management", Description: "Integration test Plugin",
		Version: "0.1.0", ConfigVersion: 1, MultipleInstances: false,
	}
}

func (*integrationDefinition) ValidateConfig(_ context.Context, config plugin.Config) error {
	var value struct {
		LoggerID string `json:"logger_id"`
	}
	if err := json.Unmarshal(config, &value); err != nil || strings.TrimSpace(value.LoggerID) == "" {
		return errors.New("logger_id is required")
	}
	return nil
}

func (definition *integrationDefinition) NewRuntime(plugin.RuntimeSpec, plugin.Host) (plugin.Runtime, error) {
	return integrationRuntime{runCount: &definition.runCount}, nil
}

type integrationRuntime struct{ runCount *atomic.Int32 }

func (runtime integrationRuntime) Run(ctx context.Context) error {
	runtime.runCount.Add(1)
	<-ctx.Done()
	return ctx.Err()
}

func waitForRunCount(t *testing.T, counter *atomic.Int32, want int32) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if counter.Load() >= want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("runtime run count = %d, want at least %d", counter.Load(), want)
}

func newPluginHTTPIntegrationDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run Plugin HTTP integration tests")
	}
	adminDatabase, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("opening PostgreSQL: %v", err)
	}
	t.Cleanup(func() { _ = adminDatabase.Close() })
	schema := "plugin_http_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
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
	migration, err := os.ReadFile("../../../migrations/000010_create_plugin_instances.up.sql")
	if err != nil {
		t.Fatalf("reading migration: %v", err)
	}
	if err := database.Exec(string(migration)).Error; err != nil {
		t.Fatalf("applying migration: %v", err)
	}
	return database
}

var _ plugin.Definition = (*integrationDefinition)(nil)
var _ plugin.Runtime = integrationRuntime{}
