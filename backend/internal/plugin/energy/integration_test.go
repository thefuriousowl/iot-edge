package energy

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/thefuriousowl/iot-edge/internal/datalogger"
	dataloggerpostgres "github.com/thefuriousowl/iot-edge/internal/datalogger/postgres"
	"github.com/thefuriousowl/iot-edge/internal/plugin"
	pluginpostgres "github.com/thefuriousowl/iot-edge/internal/plugin/postgres"
	gormpostgres "gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestEnergyDefinitionValidatesAndPersistsAgainstDataLogger_Integration(t *testing.T) {
	database := newEnergyIntegrationDatabase(t)
	tagIDs := insertEnergyTags(t, database)
	loggerRepository := dataloggerpostgres.NewRepository(database)
	startAt := time.Date(2026, time.August, 22, 0, 0, 0, 0, time.UTC)
	logger := datalogger.Logger{Name: "Energy Source Logger", Enabled: true, Timezone: "UTC", Mode: datalogger.ModeInterval, StartAt: startAt, Config: json.RawMessage(`{"interval_seconds":60}`)}
	if err := loggerRepository.Create(context.Background(), &logger, tagIDs); err != nil {
		t.Fatalf("Create(Logger) error = %v", err)
	}
	liveHub, err := NewLiveHub()
	if err != nil {
		t.Fatalf("NewLiveHub() error = %v", err)
	}
	definition, err := NewDefinition(loggerRepository, WithRuntimeFactory(liveHub))
	if err != nil {
		t.Fatalf("NewDefinition() error = %v", err)
	}
	registry, err := plugin.NewRegistry(definition)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	pluginRepository := pluginpostgres.NewRepository(database)
	service, err := plugin.NewService(pluginRepository, registry)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	config := Config{
		LoggerID:            logger.ID,
		ElectricalPowerTags: []PowerTag{{TagID: tagIDs[0], Unit: PowerUnitW}, {TagID: tagIDs[1], Unit: PowerUnitKW}},
		ThermalPowerTags:    []PowerTag{{TagID: tagIDs[2], Unit: PowerUnitMW}},
		Timezone:            "Asia/Bangkok", MaxGapSeconds: 120,
		Tariff: FlatTariff{Currency: "THB", RatePerKWh: 4.5},
	}
	instance, err := service.Create(context.Background(), plugin.CreateInput{Type: PluginType, Name: "Plant Energy", Config: encodeEnergyConfig(t, config)})
	if err != nil {
		t.Fatalf("Create(Energy Plugin) error = %v", err)
	}
	if instance.ID == uuid.Nil || instance.Enabled || instance.Type != PluginType || instance.ConfigVersion != 1 {
		t.Errorf("created Plugin = %#v", instance)
	}
	storedConfig, err := DecodeConfig(instance.Config)
	if err != nil || !reflect.DeepEqual(storedConfig, config) {
		t.Errorf("stored config = %#v, error = %v", storedConfig, err)
	}
	manifests := service.Types()
	if len(manifests) != 1 || manifests[0].Type != PluginType || len(manifests[0].Capabilities) != 1 || manifests[0].Capabilities[0] != plugin.CapabilityLoggerCommittedBatches {
		t.Errorf("Plugin types = %#v", manifests)
	}

	invalidMappings := []struct {
		name   string
		config Config
		want   error
	}{
		{name: "not selected", config: mutateConfig(config, func(value *Config) { value.ElectricalPowerTags = []PowerTag{{TagID: uuid.New(), Unit: PowerUnitKW}} }), want: ErrPowerTagNotSelected},
		{name: "bool", config: mutateConfig(config, func(value *Config) {
			value.ElectricalPowerTags = []PowerTag{{TagID: tagIDs[3], Unit: PowerUnitKW}}
			value.ThermalPowerTags = nil
		}), want: ErrPowerTagNotNumeric},
		{name: "missing Logger", config: mutateConfig(config, func(value *Config) { value.LoggerID = uuid.New() }), want: ErrLoggerUnavailable},
	}
	for _, invalid := range invalidMappings {
		t.Run(invalid.name, func(t *testing.T) {
			_, createErr := service.Create(context.Background(), plugin.CreateInput{Type: PluginType, Name: "Invalid " + invalid.name, Config: encodeEnergyConfig(t, invalid.config)})
			if !errors.Is(createErr, plugin.ErrInvalidConfig) || !errors.Is(createErr, invalid.want) {
				t.Errorf("Create() error = %v", createErr)
			}
		})
	}
	listed, err := service.List(context.Background(), plugin.ListInput{Page: 1, PerPage: 20})
	if err != nil || listed.Total != 1 {
		t.Fatalf("List() = %#v, %v; want one persisted instance", listed, err)
	}

	broker, err := datalogger.NewCommittedBatchBroker()
	if err != nil {
		t.Fatalf("NewCommittedBatchBroker() error = %v", err)
	}
	historyRepository := dataloggerpostgres.NewHistoryRepository(database, dataloggerpostgres.WithCommittedBatchPublisher(broker))
	batchFeed, err := datalogger.NewCommittedBatchFeed(historyRepository, broker)
	if err != nil {
		t.Fatalf("NewCommittedBatchFeed() error = %v", err)
	}
	host, err := plugin.NewCapabilityHost(map[plugin.Capability]any{plugin.CapabilityLoggerCommittedBatches: batchFeed})
	if err != nil {
		t.Fatalf("NewCapabilityHost() error = %v", err)
	}
	manager, err := plugin.NewManager(pluginRepository, registry, host, plugin.WithManagerReconcileInterval(0))
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
	if _, err := service.SetEnabled(context.Background(), instance.ID, true); err != nil {
		t.Fatalf("SetEnabled(true) error = %v", err)
	}
	if err := manager.Reconcile(context.Background()); err != nil {
		t.Fatalf("Manager.Reconcile() error = %v", err)
	}
	status := manager.Status(instance.ID)
	if status.State != plugin.RuntimeStateRunning || status.LastError != nil {
		t.Errorf("runtime status = %#v", status)
	}
	firstAt := startAt.Add(time.Hour)
	secondAt := firstAt.Add(time.Minute)
	writeEnergyBatch := func(batchAt time.Time, electricalW float64, electricalKW uint16, thermalMW float32) {
		t.Helper()
		if err := historyRepository.WriteBatch(context.Background(), datalogger.RawBatch{LoggerID: logger.ID, BatchAt: batchAt, Samples: []datalogger.RawSample{
			{TagID: tagIDs[0], ObservedAt: batchAt, DataType: "float64", Value: electricalW, Quality: datalogger.RawQualityGood},
			{TagID: tagIDs[1], ObservedAt: batchAt, DataType: "uint16", Value: electricalKW, Quality: datalogger.RawQualityGood},
			{TagID: tagIDs[2], ObservedAt: batchAt, DataType: "float32", Value: thermalMW, Quality: datalogger.RawQualityGood},
		}}); err != nil {
			t.Fatalf("WriteBatch(%s) error = %v", batchAt, err)
		}
	}
	writeEnergyBatch(firstAt, 1000, 2, 0.009)
	writeEnergyBatch(secondAt, 2000, 2, 0.012)
	energyService, err := NewService(pluginRepository, historyRepository, WithServiceClock(func() time.Time { return secondAt }), WithLiveHub(liveHub))
	if err != nil {
		t.Fatalf("NewService(Energy) error = %v", err)
	}
	overview, err := energyService.Overview(context.Background(), instance.ID)
	if err != nil {
		t.Fatalf("Overview() error = %v", err)
	}
	if overview.Latest == nil || overview.Latest.Electrical.Kilowatts != 4 || !closeEnergy(overview.Latest.Thermal.Kilowatts, 12) || !overview.Latest.COP.Valid || !closeEnergy(overview.Latest.COP.Value, 3) {
		t.Errorf("Overview latest = %#v", overview.Latest)
	}
	wantElectricalKWh := 3.5 / 60
	if !closeEnergy(overview.Today.Electrical.KilowattHours, wantElectricalKWh) || !closeEnergy(overview.Today.Thermal.KilowattHours, 10.5/60) || !overview.Today.COP.Valid || !closeEnergy(overview.Today.COP.Value, 3) || !overview.Today.Cost.Valid || !closeEnergy(overview.Today.Cost.Value, wantElectricalKWh*4.5) {
		t.Errorf("Overview today = %#v", overview.Today)
	}
	historyResult, err := energyService.History(context.Background(), instance.ID, HistoryInput{From: firstAt, To: secondAt, Bucket: datalogger.QueryBucket1Minute})
	if err != nil {
		t.Fatalf("History() error = %v", err)
	}
	if historyResult.Total != 1 || len(historyResult.Data) != 1 || !closeEnergy(historyResult.Data[0].Electrical.KilowattHours, wantElectricalKWh) || historyResult.Data[0].Electrical.CoveragePercent != 100 {
		t.Errorf("History() = %#v", historyResult)
	}
	var exported bytes.Buffer
	if err := energyService.ExportCSV(context.Background(), instance.ID, HistoryInput{From: firstAt, To: secondAt, Bucket: datalogger.QueryBucket1Minute}, &exported); err != nil {
		t.Fatalf("ExportCSV() error = %v", err)
	}
	if !strings.Contains(exported.String(), "electrical_kwh") || !strings.Contains(exported.String(), "100") {
		t.Errorf("Energy CSV = %q", exported.String())
	}
	liveSubscription, err := energyService.SubscribeLive(context.Background(), instance.ID, "")
	if err != nil {
		t.Fatalf("SubscribeLive() error = %v", err)
	}
	defer liveSubscription.Unsubscribe()
	if len(liveSubscription.Replay) != 1 || !liveSubscription.Replay[0].Metrics.BatchAt.Equal(secondAt) {
		t.Fatalf("live persisted replay = %#v", liveSubscription.Replay)
	}
	liveCursor := liveSubscription.Replay[0].ID
	thirdAt := secondAt.Add(time.Minute)
	writeEnergyBatch(thirdAt, 3000, 2, 0.015)
	select {
	case event := <-liveSubscription.Stream:
		if !event.Metrics.BatchAt.Equal(thirdAt) || event.Metrics.Electrical.Kilowatts != 5 || !closeEnergy(event.Metrics.COP.Value, 3) {
			t.Errorf("committed live event = %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("committed batch did not reach Energy live stream")
	}
	resumed, err := energyService.SubscribeLive(context.Background(), instance.ID, liveCursor)
	if err != nil {
		t.Fatalf("SubscribeLive(resume) error = %v", err)
	}
	if resumed.Reset || len(resumed.Replay) != 1 || !resumed.Replay[0].Metrics.BatchAt.Equal(thirdAt) {
		t.Errorf("resumed live stream = %#v", resumed)
	}
	resumed.Unsubscribe()
	if _, err := service.SetEnabled(context.Background(), instance.ID, false); err != nil {
		t.Fatalf("SetEnabled(false) error = %v", err)
	}
	if err := manager.Reconcile(context.Background()); err != nil {
		t.Fatalf("Manager.Reconcile(disabled) error = %v", err)
	}
	if err := database.Exec("DELETE FROM data_logger_tags WHERE logger_id = ? AND tag_id = ?", logger.ID, tagIDs[0]).Error; err != nil {
		t.Fatalf("removing mapped Logger Tag: %v", err)
	}
	if _, err := service.SetEnabled(context.Background(), instance.ID, true); !errors.Is(err, plugin.ErrInvalidConfig) || !errors.Is(err, ErrPowerTagNotSelected) {
		t.Errorf("SetEnabled(after Logger drift) error = %v", err)
	}
	stored, err := service.Get(context.Background(), instance.ID)
	if err != nil || stored.Enabled {
		t.Errorf("stored instance after rejected enable = %#v, %v", stored, err)
	}
}

func newEnergyIntegrationDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run Energy integration tests")
	}
	adminDatabase, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("opening PostgreSQL: %v", err)
	}
	t.Cleanup(func() { _ = adminDatabase.Close() })
	schema := "energy_plugin_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
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
	for _, migrationPath := range []string{
		"../../../migrations/000002_create_vgateways.up.sql",
		"../../../migrations/000003_create_devices_datasources.up.sql",
		"../../../migrations/000004_create_tags.up.sql",
		"../../../migrations/000006_create_data_loggers.up.sql",
		"../../../migrations/000007_create_tag_values_raw.up.sql",
		"../../../migrations/000008_add_data_logger_storage_limits.up.sql",
		"../../../migrations/000010_create_plugin_instances.up.sql",
	} {
		migration, err := os.ReadFile(migrationPath)
		if err != nil {
			t.Fatalf("reading migration %s: %v", migrationPath, err)
		}
		if err := database.Exec(string(migration)).Error; err != nil {
			t.Fatalf("applying migration %s: %v", migrationPath, err)
		}
	}
	return database
}

func insertEnergyTags(t *testing.T, database *gorm.DB) []uuid.UUID {
	t.Helper()
	dataTypes := []string{"float64", "uint16", "float32", "bool"}
	tagIDs := make([]uuid.UUID, 0, len(dataTypes))
	for index, dataType := range dataTypes {
		tagID := uuid.New()
		if err := database.Exec("INSERT INTO tags (id,name,type,data_type,enabled,config) VALUES (?,?,'constant',?,true,'{}')", tagID, "Energy Tag "+string(rune('A'+index)), dataType).Error; err != nil {
			t.Fatalf("inserting %s Tag: %v", dataType, err)
		}
		tagIDs = append(tagIDs, tagID)
	}
	return tagIDs
}
