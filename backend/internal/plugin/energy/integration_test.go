package energy

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/thefuriousowl/iot-edge/internal/datalogger"
	dataloggerpostgres "github.com/thefuriousowl/iot-edge/internal/datalogger/postgres"
	"github.com/thefuriousowl/iot-edge/internal/datalogger/tagsnapshot"
	"github.com/thefuriousowl/iot-edge/internal/device"
	devicepostgres "github.com/thefuriousowl/iot-edge/internal/device/postgres"
	"github.com/thefuriousowl/iot-edge/internal/plugin"
	pluginpostgres "github.com/thefuriousowl/iot-edge/internal/plugin/postgres"
	"github.com/thefuriousowl/iot-edge/internal/protocol/modbus"
	"github.com/thefuriousowl/iot-edge/internal/tag"
	tagpostgres "github.com/thefuriousowl/iot-edge/internal/tag/postgres"
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
	if len(manifests) != 1 || manifests[0].Type != PluginType || len(manifests[0].Capabilities) != 3 || len(manifests[0].Outputs) != 18 {
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
	outputBroker, err := plugin.NewOutputBroker()
	if err != nil {
		t.Fatalf("NewOutputBroker() error = %v", err)
	}
	outputStore, err := plugin.NewOutputStore(pluginpostgres.NewOutputRepository(database), service, outputBroker)
	if err != nil {
		t.Fatalf("NewOutputStore() error = %v", err)
	}
	host, err := plugin.NewCapabilityHost(map[plugin.Capability]any{
		plugin.CapabilityLoggerCommittedBatches: batchFeed,
		plugin.CapabilityLoggerHistoryBatches:   historyRepository,
		plugin.CapabilityPluginOutputsPublish:   outputStore,
	})
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
	latestOutput := awaitEnergyOutput(t, outputStore, instance.ID, secondAt)
	if latestOutput.Sequence != 2 || len(latestOutput.Values) != 16 || !latestOutput.Values[0].ObservedAt.Equal(secondAt) {
		t.Errorf("latest generic Energy output = %#v", latestOutput)
	}
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

func TestEnergyPipelineUsesCommittedLoggerBatchesWithoutExtraModbusReads_Integration(t *testing.T) {
	database := newEnergyIntegrationDatabase(t)
	modbusServer := newEnergyModbusServer(t, [][2]uint16{{12500, 37500}, {15000, 45000}})
	host, portText, err := net.SplitHostPort(modbusServer.listener.Addr().String())
	if err != nil {
		t.Fatalf("splitting Modbus address: %v", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("parsing Modbus port: %v", err)
	}

	gatewayID := uuid.New()
	gatewayConfig := fmt.Sprintf(`{"host":%q,"port":%d,"timeout":1000,"retry_count":0,"retry_delay":0,"keep_alive":false,"reconnect_interval":0}`, host, port)
	if err := database.Exec(`INSERT INTO vgateways (id,name,type,config) VALUES (?,?,'modbus_tcp',CAST(? AS jsonb))`, gatewayID, "Energy integration gateway", gatewayConfig).Error; err != nil {
		t.Fatalf("inserting vGateway: %v", err)
	}
	deviceRepository := devicepostgres.NewRepository(database)
	deviceService, err := device.NewService(deviceRepository, modbus.NewDefaultModbusTCPDriver())
	if err != nil {
		t.Fatalf("device.NewService() error = %v", err)
	}
	parent, err := deviceService.CreateDevice(t.Context(), gatewayID, device.CreateDeviceInput{
		Name: "Energy meter", Type: device.DeviceTypeModbus, Config: json.RawMessage(`{"unit_id":7}`),
	})
	if err != nil {
		t.Fatalf("CreateDevice() error = %v", err)
	}
	datasource, err := deviceService.CreateDatasource(t.Context(), parent.ID, device.CreateDatasourceInput{
		Name: "Synchronized power registers", Type: device.DatasourceTypeModbusRead,
		Config: json.RawMessage(`{"function_code":3,"start_address":0,"quantity":2,"poll_interval_ms":100}`),
	})
	if err != nil {
		t.Fatalf("CreateDatasource() error = %v", err)
	}

	tagRepository := tagpostgres.NewRepository(database)
	tagService, err := tag.NewService(tagRepository, deviceService, tag.NewBinaryNumericDecoder())
	if err != nil {
		t.Fatalf("tag.NewService() error = %v", err)
	}
	electricalTag, err := tagService.Create(t.Context(), tag.CreateInput{
		DatasourceID: &datasource.ID, Name: "ActivePowerTotal_W", Type: tag.TypeReading, DataType: tag.DataTypeUInt16,
		Config: json.RawMessage(`{"decoder":{"type":"binary_numeric","config":{"byte_offset":0,"byte_order":"big_endian"}}}`),
	})
	if err != nil {
		t.Fatalf("Create(electrical Tag) error = %v", err)
	}
	thermalTag, err := tagService.Create(t.Context(), tag.CreateInput{
		DatasourceID: &datasource.ID, Name: "ThermalEnergy_W", Type: tag.TypeReading, DataType: tag.DataTypeUInt16,
		Config: json.RawMessage(`{"decoder":{"type":"binary_numeric","config":{"byte_offset":2,"byte_order":"big_endian"}}}`),
	})
	if err != nil {
		t.Fatalf("Create(thermal Tag) error = %v", err)
	}

	values := tag.NewMemoryValueStore()

	loggerRepository := dataloggerpostgres.NewRepository(database)
	firstAt := time.Now().UTC().Truncate(time.Minute).Add(-time.Minute)
	secondAt := firstAt.Add(time.Minute)
	logger := datalogger.Logger{
		Name: "Energy acquisition logger", Enabled: true, Timezone: "UTC", Mode: datalogger.ModeInterval,
		StartAt: firstAt.Add(-time.Minute), Config: json.RawMessage(`{"interval_seconds":60}`),
	}
	selectedTagIDs := []uuid.UUID{electricalTag.ID, thermalTag.ID}
	if err := loggerRepository.Create(t.Context(), &logger, selectedTagIDs); err != nil {
		t.Fatalf("Create(Logger) error = %v", err)
	}
	storedLogger, err := loggerRepository.Find(t.Context(), logger.ID)
	if err != nil {
		t.Fatalf("Find(Logger) error = %v", err)
	}

	broker, err := datalogger.NewCommittedBatchBroker()
	if err != nil {
		t.Fatalf("NewCommittedBatchBroker() error = %v", err)
	}
	historyRepository := dataloggerpostgres.NewHistoryRepository(database, dataloggerpostgres.WithCommittedBatchPublisher(broker))
	feed, err := datalogger.NewCommittedBatchFeed(historyRepository, broker)
	if err != nil {
		t.Fatalf("NewCommittedBatchFeed() error = %v", err)
	}
	pluginRepository := pluginpostgres.NewRepository(database)
	firstHub, err := NewLiveHub()
	if err != nil {
		t.Fatalf("NewLiveHub() error = %v", err)
	}
	firstRegistry := newEnergyIntegrationRegistry(t, loggerRepository, firstHub)
	pluginService, err := plugin.NewService(pluginRepository, firstRegistry)
	if err != nil {
		t.Fatalf("plugin.NewService() error = %v", err)
	}
	outputBroker, err := plugin.NewOutputBroker()
	if err != nil {
		t.Fatalf("plugin.NewOutputBroker() error = %v", err)
	}
	outputStore, err := plugin.NewOutputStore(pluginpostgres.NewOutputRepository(database), pluginService, outputBroker)
	if err != nil {
		t.Fatalf("plugin.NewOutputStore() error = %v", err)
	}
	hostCapabilities, err := plugin.NewCapabilityHost(map[plugin.Capability]any{
		plugin.CapabilityLoggerCommittedBatches: feed,
		plugin.CapabilityLoggerHistoryBatches:   historyRepository,
		plugin.CapabilityPluginOutputsPublish:   outputStore,
	})
	if err != nil {
		t.Fatalf("NewCapabilityHost() error = %v", err)
	}
	instance, err := pluginService.Create(t.Context(), plugin.CreateInput{
		Type: PluginType,
		Name: "Committed pipeline energy",
		Config: encodeEnergyConfig(t, Config{
			LoggerID:            logger.ID,
			ElectricalPowerTags: []PowerTag{{TagID: electricalTag.ID, Unit: PowerUnitW}},
			ThermalPowerTags:    []PowerTag{{TagID: thermalTag.ID, Unit: PowerUnitW}},
			Timezone:            "UTC", MaxGapSeconds: 120,
			Tariff: FlatTariff{Currency: "THB", RatePerKWh: 4.5},
		}),
	})
	if err != nil {
		t.Fatalf("Create(Energy Plugin) error = %v", err)
	}
	if _, err := pluginService.SetEnabled(t.Context(), instance.ID, true); err != nil {
		t.Fatalf("SetEnabled(true) error = %v", err)
	}
	outputSubscription, err := outputStore.Subscribe(instance.ID)
	if err != nil {
		t.Fatalf("Plugin output Subscribe() error = %v", err)
	}
	t.Cleanup(outputSubscription.Close)
	firstManager := newEnergyIntegrationManager(t, pluginRepository, firstRegistry, hostCapabilities)
	t.Cleanup(func() { stopEnergyIntegrationManager(t, firstManager) })

	snapshotReader, err := tagsnapshot.NewReader(values)
	if err != nil {
		t.Fatalf("tagsnapshot.NewReader() error = %v", err)
	}
	scheduler, err := datalogger.NewIntervalScheduler(snapshotReader, historyRepository)
	if err != nil {
		t.Fatalf("NewIntervalScheduler() error = %v", err)
	}

	acquisition, err := tag.NewAcquisitionRuntime(
		tagRepository,
		tagService,
		deviceService,
		values,
		tag.WithAcquisitionReconcileInterval(0),
	)
	if err != nil {
		t.Fatalf("tag.NewAcquisitionRuntime() error = %v", err)
	}
	if err := acquisition.Start(t.Context()); err != nil {
		t.Fatalf("AcquisitionRuntime.Start() error = %v", err)
	}
	t.Cleanup(acquisition.Stop)
	modbusServer.awaitRequests(t, 1)
	awaitEnergyTagValue(t, values, electricalTag.ID, 12500)
	awaitEnergyTagValue(t, values, thermalTag.ID, 37500)
	if err := scheduler.Capture(t.Context(), *storedLogger, firstAt); err != nil {
		t.Fatalf("Capture(first batch) error = %v", err)
	}
	modbusServer.awaitRequests(t, 2)
	awaitEnergyTagValue(t, values, electricalTag.ID, 15000)
	awaitEnergyTagValue(t, values, thermalTag.ID, 45000)
	acquisition.Stop()
	if err := scheduler.Capture(t.Context(), *storedLogger, secondAt); err != nil {
		t.Fatalf("Capture(second batch) error = %v", err)
	}

	energyService, err := NewService(pluginRepository, historyRepository, WithServiceClock(func() time.Time { return secondAt }), WithLiveHub(firstHub))
	if err != nil {
		t.Fatalf("NewService(Energy) error = %v", err)
	}
	overview := awaitEnergyOverview(t, energyService, instance.ID, secondAt)
	if overview.Latest.Electrical.Kilowatts != 15 || overview.Latest.Thermal.Kilowatts != 45 || !overview.Latest.COP.Valid || overview.Latest.COP.Value != 3 {
		t.Errorf("latest Energy metrics = %#v", overview.Latest)
	}
	wantElectricalKWh := 13.75 / 60
	if !closeEnergy(overview.Today.Electrical.KilowattHours, wantElectricalKWh) || !closeEnergy(overview.Today.Thermal.KilowattHours, 41.25/60) || !overview.Today.Cost.Valid || !closeEnergy(overview.Today.Cost.Value, wantElectricalKWh*4.5) {
		t.Errorf("committed Energy summary = %#v", overview.Today)
	}
	latestOutput := awaitEnergyOutput(t, outputStore, instance.ID, secondAt)
	if latestOutput.Sequence != 2 || len(latestOutput.Values) != 16 || !latestOutput.Values[0].ObservedAt.Equal(secondAt) || latestOutput.Values[0].Value != 15.0 || latestOutput.Values[2].Value != 3.0 {
		t.Errorf("generic Energy output = %#v", latestOutput)
	}
	firstOutputEvent := awaitEnergyOutputEvent(t, outputSubscription.Events())
	secondOutputEvent := awaitEnergyOutputEvent(t, outputSubscription.Events())
	if firstOutputEvent.Sequence != 1 || !firstOutputEvent.Values[0].ObservedAt.Equal(firstAt) || secondOutputEvent.Sequence != 2 || !secondOutputEvent.Values[0].ObservedAt.Equal(secondAt) {
		t.Errorf("generic Energy output events = %#v / %#v", firstOutputEvent, secondOutputEvent)
	}
	modbusServer.assertRequests(t, 2)

	stopEnergyIntegrationManager(t, firstManager)
	secondHub, err := NewLiveHub()
	if err != nil {
		t.Fatalf("NewLiveHub(restart) error = %v", err)
	}
	secondRegistry := newEnergyIntegrationRegistry(t, loggerRepository, secondHub)
	secondManager := newEnergyIntegrationManager(t, pluginRepository, secondRegistry, hostCapabilities)
	t.Cleanup(func() { stopEnergyIntegrationManager(t, secondManager) })
	restartedService, err := NewService(pluginRepository, historyRepository, WithServiceClock(func() time.Time { return secondAt }), WithLiveHub(secondHub))
	if err != nil {
		t.Fatalf("NewService(Energy restart) error = %v", err)
	}
	replay := awaitEnergyReplay(t, restartedService, instance.ID)
	if !replay.Metrics.BatchAt.Equal(secondAt) || replay.Metrics.Electrical.Kilowatts != 15 || !replay.Metrics.COP.Valid || replay.Metrics.COP.Value != 3 {
		t.Errorf("restart replay = %#v", replay)
	}
	if restartedOutput, err := outputStore.Latest(t.Context(), instance.ID); err != nil || restartedOutput.Sequence != 2 {
		t.Errorf("restart generic output = %#v, %v", restartedOutput, err)
	}
	select {
	case unexpected := <-outputSubscription.Events():
		t.Fatalf("restart duplicate emitted output event %#v", unexpected)
	case <-time.After(20 * time.Millisecond):
	}
	modbusServer.assertRequests(t, 2)
}

func newEnergyIntegrationRegistry(t *testing.T, loggerRepository datalogger.Repository, hub *LiveHub) *plugin.Registry {
	t.Helper()
	definition, err := NewDefinition(loggerRepository, WithRuntimeFactory(hub))
	if err != nil {
		t.Fatalf("NewDefinition() error = %v", err)
	}
	registry, err := plugin.NewRegistry(definition)
	if err != nil {
		t.Fatalf("plugin.NewRegistry() error = %v", err)
	}
	return registry
}

func awaitEnergyOutput(t *testing.T, feed plugin.OutputFeed, instanceID uuid.UUID, observedAt time.Time) *plugin.OutputBatch {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		batch, err := feed.Latest(t.Context(), instanceID)
		if err == nil && batch != nil && len(batch.Values) > 0 && batch.Values[0].ObservedAt.Equal(observedAt) {
			return batch
		}
		if err != nil && !errors.Is(err, plugin.ErrOutputBatchNotFound) {
			t.Fatalf("Plugin output Latest() error = %v", err)
		}
		time.Sleep(time.Millisecond)
	}
	batch, err := feed.Latest(t.Context(), instanceID)
	t.Fatalf("Plugin output Latest() = %#v, %v; output at %s was not persisted", batch, err, observedAt)
	return nil
}

func awaitEnergyOutputEvent(t *testing.T, events <-chan plugin.OutputBatch) plugin.OutputBatch {
	t.Helper()
	select {
	case event := <-events:
		return event
	case <-time.After(time.Second):
		t.Fatal("generic Energy output event was not published")
		return plugin.OutputBatch{}
	}
}

func newEnergyIntegrationManager(t *testing.T, repository plugin.Repository, registry *plugin.Registry, host plugin.Host) *plugin.Manager {
	t.Helper()
	manager, err := plugin.NewManager(repository, registry, host, plugin.WithManagerReconcileInterval(0))
	if err != nil {
		t.Fatalf("plugin.NewManager() error = %v", err)
	}
	if err := manager.Start(t.Context()); err != nil {
		t.Fatalf("Plugin Manager Start() error = %v", err)
	}
	return manager
}

func stopEnergyIntegrationManager(t *testing.T, manager *plugin.Manager) {
	t.Helper()
	if manager == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := manager.Stop(ctx); err != nil {
		t.Errorf("Plugin Manager Stop() error = %v", err)
	}
}

func awaitEnergyTagValue(t *testing.T, store *tag.MemoryValueStore, tagID uuid.UUID, want uint16) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		value, exists := store.Latest(tagID)
		if exists && value.Quality == tag.ValueQualityGood && value.Value == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	value, _ := store.Latest(tagID)
	t.Fatalf("latest Tag %s value = %#v, want %d", tagID, value, want)
}

func awaitEnergyOverview(t *testing.T, service *Service, instanceID uuid.UUID, batchAt time.Time) *OverviewResult {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		result, err := service.Overview(t.Context(), instanceID)
		if err == nil && result.Latest != nil && result.Latest.BatchAt.Equal(batchAt) {
			return result
		}
		time.Sleep(time.Millisecond)
	}
	result, err := service.Overview(t.Context(), instanceID)
	t.Fatalf("Energy Overview() = %#v, %v; latest batch did not reach Plugin", result, err)
	return nil
}

func awaitEnergyReplay(t *testing.T, service *Service, instanceID uuid.UUID) LiveEvent {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		subscription, err := service.SubscribeLive(t.Context(), instanceID, "")
		if err != nil {
			t.Fatalf("SubscribeLive() error = %v", err)
		}
		if len(subscription.Replay) > 0 {
			event := subscription.Replay[len(subscription.Replay)-1]
			subscription.Unsubscribe()
			return event
		}
		subscription.Unsubscribe()
		time.Sleep(time.Millisecond)
	}
	t.Fatal("persisted Energy batch was not replayed after Plugin Manager restart")
	return LiveEvent{}
}

type energyModbusServer struct {
	listener  net.Listener
	responses [][2]uint16
	errors    chan error

	mu       sync.Mutex
	requests int
}

func newEnergyModbusServer(t *testing.T, responses [][2]uint16) *energyModbusServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening for Modbus integration server: %v", err)
	}
	server := &energyModbusServer{listener: listener, responses: append([][2]uint16(nil), responses...), errors: make(chan error, 8)}
	go server.serve()
	t.Cleanup(func() { _ = listener.Close() })
	return server
}

func (server *energyModbusServer) serve() {
	for {
		connection, err := server.listener.Accept()
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				server.errors <- err
			}
			return
		}
		go server.handle(connection)
	}
}

func (server *energyModbusServer) handle(connection net.Conn) {
	defer connection.Close()
	request := make([]byte, 12)
	if _, err := io.ReadFull(connection, request); err != nil {
		server.errors <- err
		return
	}
	if request[6] != 7 || request[7] != byte(modbus.FunctionReadHoldingRegisters) || binary.BigEndian.Uint16(request[8:10]) != 0 || binary.BigEndian.Uint16(request[10:12]) != 2 {
		server.errors <- fmt.Errorf("unexpected Modbus request %x", request)
		return
	}
	server.mu.Lock()
	responseIndex := server.requests
	if responseIndex >= len(server.responses) {
		server.mu.Unlock()
		server.errors <- fmt.Errorf("unexpected extra Modbus request %d", responseIndex+1)
		return
	}
	registers := server.responses[responseIndex]
	server.requests++
	server.mu.Unlock()

	response := make([]byte, 13)
	copy(response[:2], request[:2])
	binary.BigEndian.PutUint16(response[4:6], 7)
	response[6] = request[6]
	response[7] = request[7]
	response[8] = 4
	binary.BigEndian.PutUint16(response[9:11], registers[0])
	binary.BigEndian.PutUint16(response[11:13], registers[1])
	if _, err := connection.Write(response); err != nil {
		server.errors <- err
	}
}

func (server *energyModbusServer) awaitRequests(t *testing.T, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		server.mu.Lock()
		got := server.requests
		server.mu.Unlock()
		if got >= want {
			return
		}
		select {
		case err := <-server.errors:
			t.Fatalf("Modbus integration server error: %v", err)
		default:
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("Modbus requests did not reach %d", want)
}

func (server *energyModbusServer) assertRequests(t *testing.T, want int) {
	t.Helper()
	time.Sleep(20 * time.Millisecond)
	server.mu.Lock()
	got := server.requests
	server.mu.Unlock()
	if got != want {
		t.Errorf("Modbus requests = %d, want exactly %d shared Datasource polls", got, want)
	}
	select {
	case err := <-server.errors:
		t.Errorf("Modbus integration server error: %v", err)
	default:
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
		"../../../migrations/000011_create_plugin_output_latest.up.sql",
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
