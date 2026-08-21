package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/thefuriousowl/iot-edge/internal/domain"
	"github.com/thefuriousowl/iot-edge/internal/protocol"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestVGatewayRepository_CRUDIntegration(t *testing.T) {
	repository, _ := newTestVGatewayRepository(t)
	ctx := context.Background()

	gateway := newRepositoryTestVGateway("Main PLC Gateway", false)
	if err := repository.Create(ctx, gateway); err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	if gateway.ID == uuid.Nil {
		t.Fatal("Create() left gateway ID empty")
	}
	if gateway.CreatedAt.IsZero() || gateway.UpdatedAt.IsZero() {
		t.Fatalf(
			"Create() timestamps = (%v, %v), want database timestamps",
			gateway.CreatedAt,
			gateway.UpdatedAt,
		)
	}

	found, err := repository.FindByID(ctx, gateway.ID)
	if err != nil {
		t.Fatalf("FindByID() error: %v", err)
	}
	if found.Enabled {
		t.Error("FindByID() enabled = true, want explicitly persisted false")
	}
	if !reflect.DeepEqual(
		decodeRepositoryModbusConfig(t, found.Config),
		decodeRepositoryModbusConfig(t, gateway.Config),
	) {
		t.Errorf("FindByID() config = %s, want %s", found.Config, gateway.Config)
	}

	originalUpdatedAt := found.UpdatedAt
	time.Sleep(2 * time.Millisecond)
	found.Name = "Updated PLC Gateway"
	found.Description = nil
	found.Enabled = true
	found.Type = domain.VGatewayType("mqtt")
	config := decodeRepositoryModbusConfig(t, found.Config)
	config.Host = "192.168.1.101"
	config.KeepAlive = false
	found.Config = mustRepositoryConfig(t, config)
	if err := repository.Update(ctx, found); err != nil {
		t.Fatalf("Update() error: %v", err)
	}

	updated, err := repository.FindByID(ctx, gateway.ID)
	if err != nil {
		t.Fatalf("FindByID() after update: %v", err)
	}
	if updated.Name != "Updated PLC Gateway" || updated.Description != nil || !updated.Enabled {
		t.Errorf("updated gateway fields = %#v", updated)
	}
	if updated.Type != domain.VGatewayTypeModbusTCP {
		t.Errorf("Update() changed immutable type to %q", updated.Type)
	}
	updatedConfig := decodeRepositoryModbusConfig(t, updated.Config)
	if updatedConfig.Host != "192.168.1.101" || updatedConfig.KeepAlive {
		t.Errorf("updated config = %#v", updatedConfig)
	}
	if !updated.UpdatedAt.After(originalUpdatedAt) {
		t.Errorf(
			"UpdatedAt after update = %v, want after %v",
			updated.UpdatedAt,
			originalUpdatedAt,
		)
	}

	if err := repository.Delete(ctx, gateway.ID); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}
	if _, err := repository.FindByID(ctx, gateway.ID); !errors.Is(err, ErrVGatewayNotFound) {
		t.Errorf("FindByID() after delete error = %v, want ErrVGatewayNotFound", err)
	}
}

func TestVGatewayRepository_ListIntegration(t *testing.T) {
	repository, _ := newTestVGatewayRepository(t)
	ctx := context.Background()
	baseTime := time.Date(2026, time.August, 21, 8, 0, 0, 0, time.UTC)

	fixtures := []*domain.VGateway{
		newRepositoryTestVGateway("Newest B", true),
		newRepositoryTestVGateway("Newest A", false),
		newRepositoryTestVGateway("Middle", false),
		newRepositoryTestVGateway("Oldest", true),
	}
	fixtures[0].ID = uuid.MustParse("00000000-0000-0000-0000-000000000002")
	fixtures[0].CreatedAt = baseTime.Add(2 * time.Hour)
	fixtures[1].ID = uuid.MustParse("00000000-0000-0000-0000-000000000001")
	fixtures[1].CreatedAt = baseTime.Add(2 * time.Hour)
	fixtures[2].ID = uuid.MustParse("00000000-0000-0000-0000-000000000003")
	fixtures[2].CreatedAt = baseTime.Add(time.Hour)
	fixtures[3].ID = uuid.MustParse("00000000-0000-0000-0000-000000000004")
	fixtures[3].CreatedAt = baseTime

	for _, gateway := range fixtures {
		if err := repository.Create(ctx, gateway); err != nil {
			t.Fatalf("Create(%q) error: %v", gateway.Name, err)
		}
	}

	gateways, total, err := repository.List(ctx, VGatewayListOptions{
		Limit:  2,
		Offset: 1,
	})
	if err != nil {
		t.Fatalf("List() paginated error: %v", err)
	}
	if total != int64(len(fixtures)) {
		t.Errorf("List() total = %d, want %d", total, len(fixtures))
	}
	wantPageNames := []string{"Newest B", "Middle"}
	if len(gateways) != len(wantPageNames) {
		t.Fatalf("List() page length = %d, want %d", len(gateways), len(wantPageNames))
	}
	for index, wantName := range wantPageNames {
		if gateways[index].Name != wantName {
			t.Errorf("List()[%d].Name = %q, want %q", index, gateways[index].Name, wantName)
		}
	}

	disabled := false
	disabledGateways, disabledTotal, err := repository.List(ctx, VGatewayListOptions{
		Enabled: &disabled,
	})
	if err != nil {
		t.Fatalf("List() disabled filter error: %v", err)
	}
	if disabledTotal != 2 || len(disabledGateways) != 2 {
		t.Errorf(
			"List() disabled result = (%d rows, total %d), want (2, 2)",
			len(disabledGateways),
			disabledTotal,
		)
	}
	for _, gateway := range disabledGateways {
		if gateway.Enabled {
			t.Errorf("List() disabled filter returned enabled gateway %q", gateway.Name)
		}
	}

	unsupportedType := domain.VGatewayType("mqtt")
	emptyGateways, emptyTotal, err := repository.List(ctx, VGatewayListOptions{
		Type: &unsupportedType,
	})
	if err != nil {
		t.Fatalf("List() empty filter error: %v", err)
	}
	if emptyTotal != 0 || len(emptyGateways) != 0 {
		t.Errorf("List() unsupported type = (%#v, %d), want empty", emptyGateways, emptyTotal)
	}
	if emptyGateways == nil {
		t.Error("List() empty result = nil, want an empty slice")
	}
}

func TestVGatewayRepository_ErrorMappingIntegration(t *testing.T) {
	repository, _ := newTestVGatewayRepository(t)
	ctx := context.Background()

	first := newRepositoryTestVGateway("Unique Gateway", true)
	if err := repository.Create(ctx, first); err != nil {
		t.Fatalf("Create() first gateway: %v", err)
	}

	duplicate := newRepositoryTestVGateway(first.Name, true)
	if err := repository.Create(ctx, duplicate); !errors.Is(err, ErrVGatewayNameExists) {
		t.Errorf("Create() duplicate error = %v, want ErrVGatewayNameExists", err)
	}

	second := newRepositoryTestVGateway("Second Gateway", true)
	if err := repository.Create(ctx, second); err != nil {
		t.Fatalf("Create() second gateway: %v", err)
	}
	second.Name = first.Name
	if err := repository.Update(ctx, second); !errors.Is(err, ErrVGatewayNameExists) {
		t.Errorf("Update() duplicate error = %v, want ErrVGatewayNameExists", err)
	}

	missingID := uuid.New()
	if _, err := repository.FindByID(ctx, missingID); !errors.Is(err, ErrVGatewayNotFound) {
		t.Errorf("FindByID() missing error = %v, want ErrVGatewayNotFound", err)
	}
	missing := newRepositoryTestVGateway("Missing Gateway", true)
	missing.ID = missingID
	if err := repository.Update(ctx, missing); !errors.Is(err, ErrVGatewayNotFound) {
		t.Errorf("Update() missing error = %v, want ErrVGatewayNotFound", err)
	}
	if err := repository.Delete(ctx, missingID); !errors.Is(err, ErrVGatewayNotFound) {
		t.Errorf("Delete() missing error = %v, want ErrVGatewayNotFound", err)
	}
}

func TestVGatewayRepository_DeleteCascadesStatsIntegration(t *testing.T) {
	repository, db := newTestVGatewayRepository(t)
	ctx := context.Background()

	gateway := newRepositoryTestVGateway("Gateway With Stats", true)
	if err := repository.Create(ctx, gateway); err != nil {
		t.Fatalf("Create() gateway: %v", err)
	}
	stats := &domain.VGatewayStats{
		VGatewayID:    gateway.ID,
		RequestCount:  25,
		ErrorCount:    2,
		BytesReceived: 1_024,
	}
	if err := db.WithContext(ctx).Create(stats).Error; err != nil {
		t.Fatalf("creating stats: %v", err)
	}

	if err := repository.Delete(ctx, gateway.ID); err != nil {
		t.Fatalf("Delete() gateway: %v", err)
	}
	var statsCount int64
	if err := db.WithContext(ctx).
		Model(&domain.VGatewayStats{}).
		Where("vgateway_id = ?", gateway.ID).
		Count(&statsCount).Error; err != nil {
		t.Fatalf("counting stats after gateway delete: %v", err)
	}
	if statsCount != 0 {
		t.Errorf("stats after gateway delete = %d, want 0", statsCount)
	}
}

func newRepositoryTestVGateway(name string, enabled bool) *domain.VGateway {
	description := "Repository integration test gateway"
	return &domain.VGateway{
		Name:        name,
		Type:        domain.VGatewayTypeModbusTCP,
		Description: &description,
		Enabled:     enabled,
		Config: mustRepositoryConfigValue(protocol.ModbusTCPConfig{
			Host:              "192.168.1.100",
			Port:              502,
			Timeout:           5_000,
			RetryCount:        3,
			RetryDelay:        1_000,
			KeepAlive:         true,
			ReconnectInterval: 30,
		}),
	}
}

func mustRepositoryConfig(
	t *testing.T,
	config protocol.ModbusTCPConfig,
) domain.VGatewayConfig {
	t.Helper()
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("json.Marshal() config error = %v", err)
	}
	return raw
}

func mustRepositoryConfigValue(
	config protocol.ModbusTCPConfig,
) domain.VGatewayConfig {
	raw, err := json.Marshal(config)
	if err != nil {
		panic(err)
	}
	return raw
}

func decodeRepositoryModbusConfig(
	t *testing.T,
	raw domain.VGatewayConfig,
) protocol.ModbusTCPConfig {
	t.Helper()
	var config protocol.ModbusTCPConfig
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatalf("json.Unmarshal() config error = %v", err)
	}
	return config
}

func newTestVGatewayRepository(t *testing.T) (VGatewayRepository, *gorm.DB) {
	t.Helper()

	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run the PostgreSQL vGateway repository tests")
	}

	ctx := context.Background()
	adminDB, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("opening PostgreSQL connection: %v", err)
	}
	t.Cleanup(func() {
		if err := adminDB.Close(); err != nil {
			t.Errorf("closing PostgreSQL connection: %v", err)
		}
	})

	schemaName := "vgateway_repository_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := adminDB.ExecContext(ctx, "CREATE SCHEMA "+schemaName); err != nil {
		t.Fatalf("creating isolated test schema: %v", err)
	}
	t.Cleanup(func() {
		if _, err := adminDB.ExecContext(context.Background(), "DROP SCHEMA IF EXISTS "+schemaName+" CASCADE"); err != nil {
			t.Errorf("dropping isolated test schema: %v", err)
		}
	})

	testDatabaseURL, err := databaseURLWithSearchPath(databaseURL, schemaName)
	if err != nil {
		t.Fatalf("adding test schema to database URL: %v", err)
	}
	db, err := gorm.Open(postgres.Open(testDatabaseURL), &gorm.Config{})
	if err != nil {
		t.Fatalf("opening isolated GORM connection: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("getting isolated SQL connection: %v", err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("closing isolated GORM connection: %v", err)
		}
	})

	migration, err := os.ReadFile("../../migrations/000002_create_vgateways.up.sql")
	if err != nil {
		t.Fatalf("reading vGateway migration: %v", err)
	}
	if err := db.Exec(string(migration)).Error; err != nil {
		t.Fatalf("applying vGateway migration: %v", err)
	}

	return NewVGatewayRepository(db), db
}
