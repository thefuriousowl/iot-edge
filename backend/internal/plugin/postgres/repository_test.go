package pluginpostgres

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
	"github.com/thefuriousowl/iot-edge/internal/plugin"
	gormpostgres "gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestRepositoryCRUDFiltersAndDesiredState_Integration(t *testing.T) {
	database := newPluginRepositoryDatabase(t)
	repository := NewRepository(database)
	ctx := context.Background()

	energy := plugin.Instance{Type: "energy_management", Name: "Plant % Energy", Config: plugin.Config(`{"logger_id":"logger-a"}`), ConfigVersion: 1}
	if err := repository.Create(ctx, &energy); err != nil {
		t.Fatalf("Create(energy) error = %v", err)
	}
	if energy.ID == uuid.Nil || energy.Enabled || energy.CreatedAt.IsZero() || energy.UpdatedAt.IsZero() {
		t.Fatalf("created energy = %#v", energy)
	}
	found, err := repository.Find(ctx, energy.ID)
	if err != nil {
		t.Fatalf("Find(energy) error = %v", err)
	}
	assertPluginInstance(t, found, energy.ID, "energy_management", "Plant % Energy", false, 1, `{"logger_id":"logger-a"}`)

	mqtt := plugin.Instance{Type: "mqtt_publisher", Name: "MQTT_Backup", Enabled: true, Config: plugin.Config(`{"host":"broker"}`), ConfigVersion: 2}
	north := plugin.Instance{Type: "energy_management", Name: "North Energy", Enabled: true, Config: plugin.Config(`{}`), ConfigVersion: 1}
	for _, instance := range []*plugin.Instance{&mqtt, &north} {
		if err := repository.Create(ctx, instance); err != nil {
			t.Fatalf("Create(%s) error = %v", instance.Name, err)
		}
	}
	orderBase := time.Date(2026, time.August, 22, 0, 0, 0, 0, time.UTC)
	if err := database.Exec("UPDATE plugin_instances SET created_at = ? WHERE id = ?", orderBase, mqtt.ID).Error; err != nil {
		t.Fatalf("setting MQTT order fixture: %v", err)
	}
	if err := database.Exec("UPDATE plugin_instances SET created_at = ? WHERE id = ?", orderBase.Add(time.Minute), north.ID).Error; err != nil {
		t.Fatalf("setting Energy order fixture: %v", err)
	}
	assertPluginList(t, repository, plugin.ListInput{Type: pluginTypePointer("energy_management"), Page: 1, PerPage: 20}, 2)
	assertPluginList(t, repository, plugin.ListInput{Enabled: boolPointer(true), Page: 1, PerPage: 20}, 2)
	assertPluginList(t, repository, plugin.ListInput{Search: "%", Page: 1, PerPage: 20}, 1)
	assertPluginList(t, repository, plugin.ListInput{Search: "_", Page: 1, PerPage: 20}, 1)
	assertPluginList(t, repository, plugin.ListInput{Search: "ENERGY", Page: 1, PerPage: 20}, 2)
	page, err := repository.List(ctx, plugin.ListInput{Page: 2, PerPage: 1})
	if err != nil {
		t.Fatalf("List(page 2) error = %v", err)
	}
	if len(page.Data) != 1 || page.Total != 3 || page.TotalPages != 3 || page.Page != 2 || page.PerPage != 1 {
		t.Errorf("List(page 2) = %#v", page)
	}
	enabled, err := repository.ListEnabled(ctx)
	if err != nil {
		t.Fatalf("ListEnabled() error = %v", err)
	}
	if len(enabled) != 2 || enabled[0].ID != mqtt.ID || enabled[1].ID != north.ID {
		t.Errorf("ListEnabled() = %#v", enabled)
	}

	createdAt := energy.CreatedAt
	oldUpdatedAt := energy.UpdatedAt
	energy.Type = "mqtt_publisher"
	energy.Name = "Plant Energy Updated"
	energy.Enabled = true
	energy.Config = plugin.Config(`{"logger_id":"logger-b","tariff":4.2}`)
	energy.ConfigVersion = 2
	if err := repository.Update(ctx, &energy); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	updated, err := repository.Find(ctx, energy.ID)
	if err != nil {
		t.Fatalf("Find(updated) error = %v", err)
	}
	assertPluginInstance(t, updated, energy.ID, "energy_management", "Plant Energy Updated", true, 2, `{"logger_id":"logger-b","tariff":4.2}`)
	if !updated.CreatedAt.Equal(createdAt) || updated.UpdatedAt.Before(oldUpdatedAt) {
		t.Errorf("timestamps changed unexpectedly: created %s, updated %s", updated.CreatedAt, updated.UpdatedAt)
	}

	energy.Name = "mqtt_backup"
	if err := repository.Update(ctx, &energy); !errors.Is(err, plugin.ErrInstanceNameExists) {
		t.Fatalf("Update(duplicate name) error = %v", err)
	}
	rolledBack, err := repository.Find(ctx, energy.ID)
	if err != nil || rolledBack.Name != "Plant Energy Updated" {
		t.Fatalf("Find(after duplicate update) = %#v, %v", rolledBack, err)
	}

	duplicate := plugin.Instance{Type: "energy_management", Name: " NORTH ENERGY ", Config: plugin.Config(`{}`), ConfigVersion: 1}
	if err := repository.Create(ctx, &duplicate); !errors.Is(err, plugin.ErrInvalidInstance) {
		t.Fatalf("Create(untrimmed name) error = %v", err)
	}
	duplicate.Name = "north energy"
	if err := repository.Create(ctx, &duplicate); !errors.Is(err, plugin.ErrInstanceNameExists) {
		t.Fatalf("Create(duplicate name) error = %v", err)
	}
	invalidConfig := plugin.Instance{Type: "energy_management", Name: "Invalid Config", Config: plugin.Config(`[]`), ConfigVersion: 1}
	if err := repository.Create(ctx, &invalidConfig); !errors.Is(err, plugin.ErrInvalidInstance) {
		t.Fatalf("Create(array config) error = %v", err)
	}
	invalidVersion := plugin.Instance{Type: "energy_management", Name: "Invalid Version", Config: plugin.Config(`{}`)}
	if err := repository.Create(ctx, &invalidVersion); !errors.Is(err, plugin.ErrInvalidInstance) {
		t.Fatalf("Create(zero version) error = %v", err)
	}
	duplicateID := plugin.Instance{ID: north.ID, Type: "energy_management", Name: "Duplicate ID", Config: plugin.Config(`{}`), ConfigVersion: 1}
	if err := repository.Create(ctx, &duplicateID); !errors.Is(err, plugin.ErrInvalidInstance) {
		t.Fatalf("Create(duplicate ID) error = %v", err)
	}

	missingID := uuid.New()
	if _, err := repository.Find(ctx, missingID); !errors.Is(err, plugin.ErrInstanceNotFound) {
		t.Fatalf("Find(missing) error = %v", err)
	}
	if err := repository.Update(ctx, &plugin.Instance{ID: missingID, Name: "Missing", Config: plugin.Config(`{}`), ConfigVersion: 1}); !errors.Is(err, plugin.ErrInstanceNotFound) {
		t.Fatalf("Update(missing) error = %v", err)
	}
	if err := repository.Delete(ctx, missingID); !errors.Is(err, plugin.ErrInstanceNotFound) {
		t.Fatalf("Delete(missing) error = %v", err)
	}
	if err := repository.Create(ctx, nil); !errors.Is(err, plugin.ErrInvalidInstance) {
		t.Fatalf("Create(nil) error = %v", err)
	}
	if err := repository.Update(ctx, nil); !errors.Is(err, plugin.ErrInvalidInstance) {
		t.Fatalf("Update(nil) error = %v", err)
	}
	if err := repository.Delete(ctx, energy.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := repository.Find(ctx, energy.ID); !errors.Is(err, plugin.ErrInstanceNotFound) {
		t.Fatalf("Find(deleted) error = %v", err)
	}
}

func TestRepositoryHonorsCancelledContext_Integration(t *testing.T) {
	database := newPluginRepositoryDatabase(t)
	repository := NewRepository(database)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := repository.ListEnabled(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("ListEnabled(cancelled) error = %v, want context.Canceled", err)
	}
}

func assertPluginList(t *testing.T, repository plugin.Repository, input plugin.ListInput, want int) {
	t.Helper()
	result, err := repository.List(context.Background(), input)
	if err != nil {
		t.Fatalf("List(%#v) error = %v", input, err)
	}
	if len(result.Data) != want || result.Total != int64(want) {
		t.Fatalf("List(%#v) = %#v", input, result)
	}
}

func assertPluginInstance(t *testing.T, instance *plugin.Instance, id uuid.UUID, instanceType plugin.Type, name string, enabled bool, configVersion uint, config string) {
	t.Helper()
	if instance.ID != id || instance.Type != instanceType || instance.Name != name || instance.Enabled != enabled || instance.ConfigVersion != configVersion {
		t.Fatalf("instance = %#v", instance)
	}
	var actualConfig any
	var expectedConfig any
	if err := json.Unmarshal(instance.Config, &actualConfig); err != nil {
		t.Fatalf("decoding actual config: %v", err)
	}
	if err := json.Unmarshal([]byte(config), &expectedConfig); err != nil {
		t.Fatalf("decoding expected config: %v", err)
	}
	actualJSON, _ := json.Marshal(actualConfig)
	expectedJSON, _ := json.Marshal(expectedConfig)
	if string(actualJSON) != string(expectedJSON) {
		t.Errorf("config = %s, want %s", actualJSON, expectedJSON)
	}
}

func newPluginRepositoryDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run Plugin repository integration tests")
	}
	adminDatabase, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("opening PostgreSQL: %v", err)
	}
	t.Cleanup(func() { _ = adminDatabase.Close() })
	schema := "plugin_repository_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
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

func pluginTypePointer(value plugin.Type) *plugin.Type { return &value }
func boolPointer(value bool) *bool                     { return &value }
