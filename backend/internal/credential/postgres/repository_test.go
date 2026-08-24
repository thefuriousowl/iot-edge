package credentialpostgres

import (
	"bytes"
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
	"github.com/thefuriousowl/iot-edge/internal/credential"
	gormpostgres "gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestRepositoryCRUDFiltersUsageAndSecretRedaction_Integration(t *testing.T) {
	database := newCredentialRepositoryDatabase(t)
	repository := NewRepository(database)
	ctx := context.Background()
	description := "Primary broker credentials"
	profiles := []*credential.Profile{
		{Type: credential.TypeMQTT, Name: "Plant % MQTT", Description: &description},
		{Type: credential.TypeHTTP, Name: "HTTP_Backup"},
		{Type: credential.TypeMQTT, Name: `Windows\Broker`},
		{Type: credential.TypeMQTT, Name: "Plant Secondary"},
	}
	for _, profile := range profiles {
		if err := repository.Create(ctx, profile); err != nil {
			t.Fatalf("Create(%s) error = %v", profile.Name, err)
		}
		if profile.ID == uuid.Nil || profile.SecretRevision != 0 || profile.CreatedAt.IsZero() || profile.UpdatedAt.IsZero() {
			t.Fatalf("created Profile = %#v", profile)
		}
	}
	orderBase := time.Date(2026, time.August, 24, 1, 0, 0, 0, time.UTC)
	for index, profile := range profiles {
		if err := database.Exec("UPDATE credential_profiles SET created_at=?,updated_at=? WHERE id=?", orderBase.Add(time.Duration(index)*time.Minute), orderBase.Add(time.Duration(index)*time.Minute), profile.ID).Error; err != nil {
			t.Fatalf("setting Profile order: %v", err)
		}
	}
	insertCredentialSecret(t, database, profiles[0].ID, "mqtt.password")
	if err := database.Exec("UPDATE credential_profiles SET secret_revision=7 WHERE id=?", profiles[0].ID).Error; err != nil {
		t.Fatalf("setting secret revision: %v", err)
	}
	insertCredentialPublisher(t, database, profiles[0].ID, "Linked MQTT")

	found, err := repository.Find(ctx, profiles[0].ID)
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	if found.Type != credential.TypeMQTT || found.Name != "Plant % MQTT" || found.Description == nil || *found.Description != description || found.SecretRevision != 7 || found.UsageCount != 1 || len(found.Secrets) != 0 {
		t.Fatalf("Find() = %#v", found)
	}
	serialized, err := json.Marshal(found)
	if err != nil {
		t.Fatalf("marshaling Profile: %v", err)
	}
	for _, forbidden := range []string{"ciphertext", "key_id", "mqtt.password"} {
		if bytes.Contains(serialized, []byte(forbidden)) {
			t.Errorf("Profile JSON exposed %q: %s", forbidden, serialized)
		}
	}

	assertCredentialList(t, repository, credential.ListInput{Type: credentialTypePointer(credential.TypeHTTP)}, []uuid.UUID{profiles[1].ID})
	assertCredentialList(t, repository, credential.ListInput{Search: "%"}, []uuid.UUID{profiles[0].ID})
	assertCredentialList(t, repository, credential.ListInput{Search: "_"}, []uuid.UUID{profiles[1].ID})
	assertCredentialList(t, repository, credential.ListInput{Search: `\`}, []uuid.UUID{profiles[2].ID})
	assertCredentialList(t, repository, credential.ListInput{Search: `' OR 1=1 --`}, []uuid.UUID{})
	assertCredentialList(t, repository, credential.ListInput{Search: "PLANT"}, []uuid.UUID{profiles[0].ID, profiles[3].ID})
	assertCredentialList(t, repository, credential.ListInput{}, []uuid.UUID{profiles[0].ID, profiles[1].ID, profiles[2].ID, profiles[3].ID})

	createdAt := found.CreatedAt
	updatedBefore := found.UpdatedAt
	found.Type = credential.TypeHTTP
	found.Name = "Plant MQTT Updated"
	found.Description = nil
	found.SecretRevision = 999
	found.UsageCount = 999
	if err := repository.Update(ctx, found); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	updated, err := repository.Find(ctx, found.ID)
	if err != nil {
		t.Fatalf("Find(updated) error = %v", err)
	}
	if updated.Type != credential.TypeMQTT || updated.Name != "Plant MQTT Updated" || updated.Description != nil || updated.SecretRevision != 7 || updated.UsageCount != 1 || !updated.CreatedAt.Equal(createdAt) || updated.UpdatedAt.Before(updatedBefore) {
		t.Fatalf("updated Profile = %#v", updated)
	}

	updated.Name = "http_backup"
	if err := repository.Update(ctx, updated); !errors.Is(err, credential.ErrNameExists) {
		t.Fatalf("Update(duplicate name) error = %v", err)
	}
	rolledBack, err := repository.Find(ctx, updated.ID)
	if err != nil || rolledBack.Name != "Plant MQTT Updated" || rolledBack.Description != nil {
		t.Fatalf("Find(after duplicate update) = %#v, %v", rolledBack, err)
	}

	invalid := credential.Profile{Type: "smtp", Name: "Unsupported"}
	if err := repository.Create(ctx, &invalid); !errors.Is(err, credential.ErrInvalid) {
		t.Errorf("Create(unsupported type) error = %v", err)
	}
	invalid = credential.Profile{Type: credential.TypeMQTT, Name: " Untrimmed "}
	if err := repository.Create(ctx, &invalid); !errors.Is(err, credential.ErrInvalid) {
		t.Errorf("Create(untrimmed name) error = %v", err)
	}
	duplicate := credential.Profile{Type: credential.TypeMQTT, Name: "HTTP_BACKUP"}
	if err := repository.Create(ctx, &duplicate); !errors.Is(err, credential.ErrNameExists) {
		t.Errorf("Create(duplicate name) error = %v", err)
	}
	duplicateID := credential.Profile{ID: profiles[1].ID, Type: credential.TypeHTTP, Name: "Duplicate ID"}
	if err := repository.Create(ctx, &duplicateID); !errors.Is(err, credential.ErrInvalid) {
		t.Errorf("Create(duplicate ID) error = %v", err)
	}

	missingID := uuid.New()
	if _, err := repository.Find(ctx, missingID); !errors.Is(err, credential.ErrNotFound) {
		t.Errorf("Find(missing) error = %v", err)
	}
	if err := repository.Update(ctx, &credential.Profile{ID: missingID, Name: "Missing"}); !errors.Is(err, credential.ErrNotFound) {
		t.Errorf("Update(missing) error = %v", err)
	}
	if err := repository.Delete(ctx, missingID); !errors.Is(err, credential.ErrNotFound) {
		t.Errorf("Delete(missing) error = %v", err)
	}
	if err := repository.Delete(ctx, profiles[3].ID); err != nil {
		t.Fatalf("Delete(unused) error = %v", err)
	}
	if _, err := repository.Find(ctx, profiles[3].ID); !errors.Is(err, credential.ErrNotFound) {
		t.Errorf("Find(deleted) error = %v", err)
	}
}

func TestRepositoryListCapsResultsAtTwoHundred_Integration(t *testing.T) {
	database := newCredentialRepositoryDatabase(t)
	repository := NewRepository(database)
	if err := database.Exec(`
		INSERT INTO credential_profiles (type,name,created_at,updated_at)
		SELECT 'mqtt', 'Profile ' || LPAD(value::text, 3, '0'),
			TIMESTAMPTZ '2026-08-24 00:00:00Z' + value * INTERVAL '1 microsecond',
			TIMESTAMPTZ '2026-08-24 00:00:00Z' + value * INTERVAL '1 microsecond'
		FROM generate_series(1,205) AS value
	`).Error; err != nil {
		t.Fatalf("creating Profile fixtures: %v", err)
	}
	profiles, err := repository.List(context.Background(), credential.ListInput{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(profiles) != 200 || profiles[0].Name != "Profile 001" || profiles[199].Name != "Profile 200" {
		t.Fatalf("List() returned %d Profiles, boundaries %q/%q", len(profiles), profiles[0].Name, profiles[len(profiles)-1].Name)
	}
}

func TestRepositoryProtectsReferencesAndCascadesSecrets_Integration(t *testing.T) {
	database := newCredentialRepositoryDatabase(t)
	repository := NewRepository(database)
	ctx := context.Background()

	unused := credential.Profile{Type: credential.TypeMQTT, Name: "Unused Profile"}
	if err := repository.Create(ctx, &unused); err != nil {
		t.Fatalf("Create(unused) error = %v", err)
	}
	insertCredentialSecret(t, database, unused.ID, "mqtt.username")
	insertCredentialSecret(t, database, unused.ID, "mqtt.password")
	if err := repository.Delete(ctx, unused.ID); err != nil {
		t.Fatalf("Delete(unused) error = %v", err)
	}
	assertCredentialRowCount(t, database, "credential_secrets", "credential_id", unused.ID, 0)

	inUse := credential.Profile{Type: credential.TypeMQTT, Name: "Referenced Profile"}
	if err := repository.Create(ctx, &inUse); err != nil {
		t.Fatalf("Create(in-use) error = %v", err)
	}
	insertCredentialSecret(t, database, inUse.ID, "mqtt.password")
	publisherID := insertCredentialPublisher(t, database, inUse.ID, "Uses Credential")
	if err := repository.Delete(ctx, inUse.ID); !errors.Is(err, credential.ErrInUse) {
		t.Fatalf("Delete(in-use) error = %v, want %v", err, credential.ErrInUse)
	}
	assertCredentialRowCount(t, database, "credential_profiles", "id", inUse.ID, 1)
	assertCredentialRowCount(t, database, "credential_secrets", "credential_id", inUse.ID, 1)
	assertCredentialRowCount(t, database, "data_publishers", "id", publisherID, 1)

	if err := database.Exec("DELETE FROM data_publishers WHERE id=?", publisherID).Error; err != nil {
		t.Fatalf("deleting Publisher fixture: %v", err)
	}
	if err := repository.Delete(ctx, inUse.ID); err != nil {
		t.Fatalf("Delete(after Publisher removal) error = %v", err)
	}
	assertCredentialRowCount(t, database, "credential_secrets", "credential_id", inUse.ID, 0)
}

func TestRepositoryRejectsInvalidInputsAndHonorsCancellation_Integration(t *testing.T) {
	database := newCredentialRepositoryDatabase(t)
	store := NewRepository(database)
	ctx := context.Background()
	profile := credential.Profile{Type: credential.TypeMQTT, Name: "Cancellation Profile"}
	if err := store.Create(ctx, &profile); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	var nilRepository *repository
	for name, operation := range map[string]func() error{
		"nil receiver":   func() error { return nilRepository.Create(ctx, &credential.Profile{}) },
		"nil database":   func() error { return NewRepository(nil).Create(ctx, &credential.Profile{}) },
		"nil context":    func() error { return store.Create(nil, &credential.Profile{}) },
		"nil create":     func() error { return store.Create(ctx, nil) },
		"nil update":     func() error { return store.Update(ctx, nil) },
		"zero update ID": func() error { return store.Update(ctx, &credential.Profile{}) },
		"zero delete ID": func() error { return store.Delete(ctx, uuid.Nil) },
	} {
		t.Run(name, func(t *testing.T) {
			if err := operation(); !errors.Is(err, credential.ErrInvalidInput) {
				t.Errorf("error = %v, want %v", err, credential.ErrInvalidInput)
			}
		})
	}
	if _, err := store.Find(ctx, uuid.Nil); !errors.Is(err, credential.ErrInvalidInput) {
		t.Errorf("Find(zero ID) error = %v", err)
	}
	if _, err := store.List(nil, credential.ListInput{}); !errors.Is(err, credential.ErrInvalidInput) {
		t.Errorf("List(nil context) error = %v", err)
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	cancelledCreate := credential.Profile{Type: credential.TypeMQTT, Name: "Cancelled Create"}
	for name, operation := range map[string]func() error{
		"create": func() error { return store.Create(cancelled, &cancelledCreate) },
		"find": func() error {
			_, err := store.Find(cancelled, profile.ID)
			return err
		},
		"list": func() error {
			_, err := store.List(cancelled, credential.ListInput{})
			return err
		},
		"update": func() error {
			copy := profile
			copy.Name = "Cancelled Update"
			return store.Update(cancelled, &copy)
		},
		"delete": func() error { return store.Delete(cancelled, profile.ID) },
	} {
		t.Run("cancelled "+name, func(t *testing.T) {
			if err := operation(); !errors.Is(err, context.Canceled) {
				t.Errorf("error = %v, want context.Canceled", err)
			}
		})
	}
	reloaded, err := store.Find(ctx, profile.ID)
	if err != nil || reloaded.Name != profile.Name {
		t.Fatalf("Profile changed after cancelled operations: %#v, %v", reloaded, err)
	}
}

func assertCredentialList(t *testing.T, repository credential.Repository, input credential.ListInput, want []uuid.UUID) {
	t.Helper()
	profiles, err := repository.List(context.Background(), input)
	if err != nil {
		t.Fatalf("List(%#v) error = %v", input, err)
	}
	if len(profiles) != len(want) {
		t.Fatalf("List(%#v) returned %d Profiles, want %d: %#v", input, len(profiles), len(want), profiles)
	}
	for index := range want {
		if profiles[index].ID != want[index] {
			t.Errorf("List(%#v)[%d] ID = %s, want %s", input, index, profiles[index].ID, want[index])
		}
	}
}

func assertCredentialRowCount(t *testing.T, database *gorm.DB, tableName, columnName string, id uuid.UUID, want int64) {
	t.Helper()
	var count int64
	if err := database.Table(tableName).Where(columnName+" = ?", id).Count(&count).Error; err != nil {
		t.Fatalf("counting %s: %v", tableName, err)
	}
	if count != want {
		t.Errorf("%s count = %d, want %d", tableName, count, want)
	}
}

func insertCredentialSecret(t *testing.T, database *gorm.DB, credentialID uuid.UUID, name string) {
	t.Helper()
	if err := database.Exec(`
		INSERT INTO credential_secrets (credential_id,name,kind,key_id,ciphertext)
		VALUES (?,?,?,?,?)
	`, credentialID, name, "opaque", "test-key", bytes.Repeat([]byte{0x7f}, 32)).Error; err != nil {
		t.Fatalf("creating Credential secret fixture: %v", err)
	}
}

func insertCredentialPublisher(t *testing.T, database *gorm.DB, credentialID uuid.UUID, name string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := database.Raw(`
		INSERT INTO data_publishers (type,name,config,config_version,credential_id)
		VALUES ('mqtt',?,'{}',1,?) RETURNING id
	`, name, credentialID).Row().Scan(&id); err != nil {
		t.Fatalf("creating Publisher fixture: %v", err)
	}
	return id
}

func newCredentialRepositoryDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run Credential repository integration tests")
	}
	adminDatabase, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("opening PostgreSQL: %v", err)
	}
	t.Cleanup(func() { _ = adminDatabase.Close() })
	schema := "credential_repository_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
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
	return database
}

func credentialTypePointer(value credential.Type) *credential.Type { return &value }
