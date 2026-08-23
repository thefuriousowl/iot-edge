package publisherpostgres

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/thefuriousowl/iot-edge/internal/publisher"
	gormpostgres "gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestPublisherRepositoryCRUDSourcesFiltersAndOwnership_Integration(t *testing.T) {
	database, tagID, pluginID := newPublisherRepositoryDatabase(t)
	repository := NewRepository(database)
	ctx := context.Background()
	tagSource := publisher.SourceSelection{Alias: "voltage", Reference: publisher.TagSource(tagID)}
	outputSource := publisher.SourceSelection{Alias: "energy", Reference: publisher.PluginOutputSource(pluginID, "today.energy_kwh")}

	plant := publisher.Publisher{
		Type: publisher.TypeHTTPServer, Name: "Plant % API", Config: publisher.Config(`{}`), ConfigVersion: 1,
		Sources: []publisher.SourceSelection{tagSource, outputSource},
	}
	if err := repository.Create(ctx, &plant); err != nil {
		t.Fatalf("Create(plant) error = %v", err)
	}
	if plant.ID == uuid.Nil || plant.Enabled || plant.CreatedAt.IsZero() || plant.UpdatedAt.IsZero() {
		t.Fatalf("created Publisher = %#v", plant)
	}
	found, err := repository.Find(ctx, plant.ID)
	if err != nil {
		t.Fatalf("Find(plant) error = %v", err)
	}
	assertPublisher(t, found, plant.ID, publisher.TypeHTTPServer, "Plant % API", false, []publisher.SourceSelection{tagSource, outputSource})

	backup := publisher.Publisher{
		Type: publisher.TypeMQTT, Name: "MQTT_Backup", Enabled: true,
		Config: publisher.Config(`{}`), ConfigVersion: 1, Sources: []publisher.SourceSelection{tagSource},
	}
	httpClient := publisher.Publisher{
		Type: publisher.TypeHTTPClient, Name: "Outbound HTTP", Enabled: true,
		Config: publisher.Config(`{}`), ConfigVersion: 1, Sources: []publisher.SourceSelection{outputSource},
	}
	for _, entity := range []*publisher.Publisher{&backup, &httpClient} {
		if err := repository.Create(ctx, entity); err != nil {
			t.Fatalf("Create(%s) error = %v", entity.Name, err)
		}
	}
	orderBase := time.Date(2026, time.August, 23, 0, 0, 0, 0, time.UTC)
	if err := database.Exec("UPDATE data_publishers SET created_at=? WHERE id=?", orderBase, backup.ID).Error; err != nil {
		t.Fatalf("setting backup order: %v", err)
	}
	if err := database.Exec("UPDATE data_publishers SET created_at=? WHERE id=?", orderBase.Add(time.Minute), httpClient.ID).Error; err != nil {
		t.Fatalf("setting HTTP Client order: %v", err)
	}
	assertPublisherList(t, repository, publisher.ListInput{Type: publisherTypePointer(publisher.TypeHTTPServer), Page: 1, PerPage: 20}, 1)
	assertPublisherList(t, repository, publisher.ListInput{Enabled: publisherBoolPointer(true), Page: 1, PerPage: 20}, 2)
	assertPublisherList(t, repository, publisher.ListInput{Search: "%", Page: 1, PerPage: 20}, 1)
	assertPublisherList(t, repository, publisher.ListInput{Search: "_", Page: 1, PerPage: 20}, 1)
	assertPublisherList(t, repository, publisher.ListInput{Search: "outbound", Page: 1, PerPage: 20}, 1)
	page, err := repository.List(ctx, publisher.ListInput{Page: 2, PerPage: 1})
	if err != nil || len(page.Data) != 1 || page.Total != 3 || page.TotalPages != 3 || page.Data[0].SourceCount < 1 || len(page.Data[0].Sources) != 0 {
		t.Fatalf("List(page 2) = %#v, %v", page, err)
	}
	enabled, err := repository.ListEnabled(ctx)
	if err != nil || len(enabled) != 2 || enabled[0].ID != backup.ID || enabled[1].ID != httpClient.ID || len(enabled[0].Sources) != 1 || len(enabled[1].Sources) != 1 {
		t.Fatalf("ListEnabled() = %#v, %v", enabled, err)
	}

	createdAt := found.CreatedAt
	found.Type = publisher.TypeMQTT
	found.Name = "Plant API Updated"
	found.Enabled = true
	found.Sources = []publisher.SourceSelection{outputSource}
	if err := repository.Update(ctx, found); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	updated, err := repository.Find(ctx, plant.ID)
	if err != nil {
		t.Fatalf("Find(updated) error = %v", err)
	}
	assertPublisher(t, updated, plant.ID, publisher.TypeHTTPServer, "Plant API Updated", true, []publisher.SourceSelection{outputSource})
	if !updated.CreatedAt.Equal(createdAt) || updated.UpdatedAt.Before(found.UpdatedAt) {
		t.Errorf("timestamps changed unexpectedly: %#v", updated)
	}

	updated.Name = "mqtt_backup"
	updated.Sources = []publisher.SourceSelection{tagSource}
	if err := repository.Update(ctx, updated); !errors.Is(err, publisher.ErrPublisherNameExists) {
		t.Fatalf("Update(duplicate name) error = %v", err)
	}
	rolledBack, err := repository.Find(ctx, plant.ID)
	if err != nil || rolledBack.Name != "Plant API Updated" || len(rolledBack.Sources) != 1 || rolledBack.Sources[0].Alias != "energy" {
		t.Fatalf("Find(after rollback) = %#v, %v", rolledBack, err)
	}

	invalid := publisher.Publisher{Type: publisher.TypeMQTT, Name: "No Sources", Config: publisher.Config(`{}`), ConfigVersion: 1}
	if err := repository.Create(ctx, &invalid); !errors.Is(err, publisher.ErrInvalidPublisher) {
		t.Errorf("Create(no sources) error = %v", err)
	}
	if _, err := repository.Find(ctx, invalid.ID); !errors.Is(err, publisher.ErrPublisherNotFound) {
		t.Errorf("invalid Create transaction persisted Publisher: %v", err)
	}
	missingSource := publisher.Publisher{
		Type: publisher.TypeMQTT, Name: "Missing Source", Config: publisher.Config(`{}`), ConfigVersion: 1,
		Sources: []publisher.SourceSelection{{Alias: "missing", Reference: publisher.TagSource(uuid.New())}},
	}
	if err := repository.Create(ctx, &missingSource); !errors.Is(err, publisher.ErrSourceNotFound) {
		t.Errorf("Create(missing source) error = %v", err)
	}
	duplicate := publisher.Publisher{
		Type: publisher.TypeMQTT, Name: "plant api updated", Config: publisher.Config(`{}`), ConfigVersion: 1,
		Sources: []publisher.SourceSelection{tagSource},
	}
	if err := repository.Create(ctx, &duplicate); !errors.Is(err, publisher.ErrPublisherNameExists) {
		t.Errorf("Create(duplicate name) error = %v", err)
	}
	if err := repository.Create(ctx, nil); !errors.Is(err, publisher.ErrInvalidPublisher) {
		t.Errorf("Create(nil) error = %v", err)
	}
	if err := repository.Update(ctx, nil); !errors.Is(err, publisher.ErrInvalidPublisher) {
		t.Errorf("Update(nil) error = %v", err)
	}

	missingID := uuid.New()
	if _, err := repository.Find(ctx, missingID); !errors.Is(err, publisher.ErrPublisherNotFound) {
		t.Errorf("Find(missing) error = %v", err)
	}
	if err := repository.Update(ctx, &publisher.Publisher{ID: missingID, Name: "Missing", Config: publisher.Config(`{}`), ConfigVersion: 1, Sources: []publisher.SourceSelection{tagSource}}); !errors.Is(err, publisher.ErrPublisherNotFound) {
		t.Errorf("Update(missing) error = %v", err)
	}
	if err := repository.Delete(ctx, missingID); !errors.Is(err, publisher.ErrPublisherNotFound) {
		t.Errorf("Delete(missing) error = %v", err)
	}

	if err := repository.Delete(ctx, plant.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := repository.Find(ctx, plant.ID); !errors.Is(err, publisher.ErrPublisherNotFound) {
		t.Errorf("Find(deleted) error = %v", err)
	}
	for tableName, id := range map[string]uuid.UUID{"tags": tagID, "plugin_instances": pluginID} {
		var count int64
		if err := database.Table(tableName).Where("id = ?", id).Count(&count).Error; err != nil || count != 1 {
			t.Errorf("%s after Publisher delete = %d, %v", tableName, count, err)
		}
	}
}

func TestPublisherRepositoryHonorsCancelledContext_Integration(t *testing.T) {
	database, _, _ := newPublisherRepositoryDatabase(t)
	repository := NewRepository(database)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := repository.ListEnabled(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("ListEnabled(cancelled) error = %v", err)
	}
}

func assertPublisher(t *testing.T, entity *publisher.Publisher, id uuid.UUID, publisherType publisher.Type, name string, enabled bool, sources []publisher.SourceSelection) {
	t.Helper()
	if entity.ID != id || entity.Type != publisherType || entity.Name != name || entity.Enabled != enabled || string(entity.Config) != `{}` || entity.ConfigVersion != 1 || entity.SourceCount != len(sources) || len(entity.Sources) != len(sources) {
		t.Fatalf("Publisher = %#v", entity)
	}
	for index := range sources {
		if entity.Sources[index] != sources[index] {
			t.Errorf("source %d = %#v, want %#v", index, entity.Sources[index], sources[index])
		}
	}
}

func assertPublisherList(t *testing.T, repository publisher.Repository, input publisher.ListInput, want int) {
	t.Helper()
	result, err := repository.List(context.Background(), input)
	if err != nil || len(result.Data) != want || result.Total != int64(want) {
		t.Fatalf("List(%#v) = %#v, %v", input, result, err)
	}
}

func newPublisherRepositoryDatabase(t *testing.T) (*gorm.DB, uuid.UUID, uuid.UUID) {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run Publisher repository integration tests")
	}
	adminDatabase, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("opening PostgreSQL: %v", err)
	}
	t.Cleanup(func() { _ = adminDatabase.Close() })
	schema := "publisher_repository_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := adminDatabase.Exec("CREATE SCHEMA " + schema); err != nil {
		t.Fatalf("creating schema: %v", err)
	}
	t.Cleanup(func() { _, _ = adminDatabase.Exec("DROP SCHEMA IF EXISTS " + schema + " CASCADE") })
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
		"../../../migrations/000010_create_plugin_instances.up.sql",
		"../../../migrations/000012_create_data_publishers.up.sql",
		"../../../migrations/000013_create_data_publisher_secrets.up.sql",
		"../../../migrations/000014_replace_modbus_publisher_with_http_client.up.sql",
	} {
		contents, err := os.ReadFile(migration)
		if err != nil {
			t.Fatalf("reading migration %s: %v", migration, err)
		}
		if err := database.Exec(string(contents)).Error; err != nil {
			t.Fatalf("applying migration %s: %v", migration, err)
		}
	}
	var tagID uuid.UUID
	if err := database.Raw(`INSERT INTO tags (name,type,data_type,config) VALUES ('Publisher Voltage','constant','float64','{"value":230}') RETURNING id`).Row().Scan(&tagID); err != nil {
		t.Fatalf("creating Tag fixture: %v", err)
	}
	var pluginID uuid.UUID
	if err := database.Raw(`INSERT INTO plugin_instances (type,name,config_version) VALUES ('energy_management','Publisher Energy',1) RETURNING id`).Row().Scan(&pluginID); err != nil {
		t.Fatalf("creating Plugin fixture: %v", err)
	}
	return database, tagID, pluginID
}

func publisherTypePointer(value publisher.Type) *publisher.Type { return &value }
func publisherBoolPointer(value bool) *bool                     { return &value }
