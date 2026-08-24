package publisher

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/device"
	"github.com/thefuriousowl/iot-edge/internal/plugin"
	"github.com/thefuriousowl/iot-edge/internal/protocol/modbus"
	"github.com/thefuriousowl/iot-edge/internal/tag"
)

func TestPublisherActivityNeverCreatesModbusAcquisitionReads(t *testing.T) {
	gatewayID := uuid.New()
	deviceID := uuid.New()
	datasourceID := uuid.New()
	readingTag := tag.Tag{
		ID: uuid.New(), DatasourceID: &datasourceID, Name: "Register zero", Type: tag.TypeReading,
		DataType: tag.DataTypeUInt16, Enabled: true,
		Config: json.RawMessage(`{"decoder":{"type":"binary_numeric","config":{"byte_offset":0}}}`),
	}
	deviceRepository := &publisherAcquisitionDeviceRepository{datasource: device.DatasourceContext{
		Datasource: device.Datasource{
			ID: datasourceID, DeviceID: deviceID, Name: "Holding register", Type: device.DatasourceTypeModbusRead, Enabled: true,
			Config: json.RawMessage(`{"function_code":3,"start_address":0,"quantity":1,"poll_interval_ms":60000}`),
		},
		Device: device.DeviceContext{
			Device: device.Device{
				ID: deviceID, VGatewayID: gatewayID, Name: "Slave", Type: device.DeviceTypeModbus, Enabled: true,
				Config: json.RawMessage(`{"unit_id":7,"request_timeout_ms":null}`),
			},
			Gateway: device.GatewayContext{
				ID: gatewayID, Type: "modbus_tcp", Enabled: true,
				Config: json.RawMessage(`{"host":"plc.local","port":502,"timeout":5000,"retry_count":0,"retry_delay":0,"keep_alive":true,"reconnect_interval":0}`),
			},
		},
	}}
	client := newPublisherCountingModbusClient()
	var clientCreations atomic.Int32
	driver, err := modbus.NewModbusTCPDriver(func(modbus.ModbusTCPConfig) (modbus.ModbusClient, error) {
		clientCreations.Add(1)
		return client, nil
	})
	if err != nil {
		t.Fatalf("NewModbusTCPDriver() error = %v", err)
	}
	deviceService, err := device.NewService(deviceRepository, driver)
	if err != nil {
		t.Fatalf("device.NewService() error = %v", err)
	}
	tagRepository := &publisherAcquisitionTagRepository{entity: readingTag}
	tagService, err := tag.NewService(tagRepository, deviceService, tag.NewBinaryNumericDecoder())
	if err != nil {
		t.Fatalf("tag.NewService() error = %v", err)
	}
	tagValues := tag.NewMemoryValueStore()
	acquisition, err := tag.NewAcquisitionRuntime(tagRepository, tagService, deviceService, tagValues, tag.WithAcquisitionReconcileInterval(0))
	if err != nil {
		t.Fatalf("tag.NewAcquisitionRuntime() error = %v", err)
	}
	if err := acquisition.Start(t.Context()); err != nil {
		t.Fatalf("acquisition.Start() error = %v", err)
	}
	defer acquisition.Stop()

	initialRead := awaitPublisherModbusRead(t, client)
	if initialRead.request != (modbus.ReadRequest{UnitID: 7, FunctionCode: modbus.FunctionReadHoldingRegisters, Address: 0, Quantity: 1}) {
		t.Fatalf("initial Modbus request = %#v", initialRead.request)
	}
	initialRead.respond <- []byte{0, 42}
	readingValue := awaitPublisherTagValue(t, tagValues, readingTag.ID, uint16(42))

	instanceID := uuid.New()
	pluginCatalog := &sourceTestPluginCatalog{
		instances: []plugin.Instance{{ID: instanceID, Type: "energy_management", Name: "Energy", Enabled: true}},
		outputs: map[uuid.UUID][]plugin.OutputDescriptor{instanceID: {
			{Key: "demand_kw", Name: "Demand", SchemaVersion: 1, DataType: plugin.OutputDataTypeFloat64, Unit: "kW", PeriodKind: plugin.OutputPeriodInstantaneous},
			{Key: "today.energy_kwh", Name: "Today energy", SchemaVersion: 1, DataType: plugin.OutputDataTypeFloat64, Unit: "kWh", PeriodKind: plugin.OutputPeriodWindowed},
		}},
	}
	pluginValues := newSourceTestPluginFeed(t)
	initialBatch := sourceTestOutputBatch(instanceID, 7, readingValue.ObservedAt)
	pluginValues.setLatest(initialBatch)
	sourceFeed, err := NewSourceFeed(tagRepository, tagValues, pluginCatalog, pluginValues)
	if err != nil {
		t.Fatalf("NewSourceFeed() error = %v", err)
	}

	readingReference := TagSource(readingTag.ID)
	energyReference := PluginOutputSource(instanceID, "today.energy_kwh")
	selections := []SourceSelection{
		{Alias: "reading", Reference: readingReference},
		{Alias: "energy", Reference: energyReference},
	}
	repository := newSourceTestPublisherRepository()
	httpPublisher := publisherAcquisitionHTTPPublisher(t, selections)
	mqttPublisher := managerPublisher(
		uuid.New(), TypeMQTT,
		Config(`{"trigger":{"mode":"on_change","source_alias":"energy","coalesce_ms":1}}`),
		selections,
	)
	for _, entity := range []*Publisher{&httpPublisher, &mqttPublisher} {
		if err := repository.Create(context.Background(), entity); err != nil {
			t.Fatalf("Create(%s) error = %v", entity.Type, err)
		}
	}

	listeners := make(chan publisherListenerResult, 4)
	resolver := &secretTestResolver{publisherID: httpPublisher.ID, materials: map[string]secretTestResolved{}}
	httpFactory, err := NewHTTPTransportFactory(resolver, NewJSONPayloadEngine(), WithHTTPListenFunc(func(_, _ string) (net.Listener, error) {
		listener, listenErr := net.Listen("tcp", "127.0.0.1:0")
		listeners <- publisherListenerResult{listener: listener, err: listenErr}
		return listener, listenErr
	}))
	if err != nil {
		t.Fatalf("NewHTTPTransportFactory() error = %v", err)
	}
	mqttFactory := newManagerTransportFactory()
	definitions, _ := NewDefaultDefinitionRegistry()
	transports := NewTransportRegistry()
	if err := transports.Register(TypeHTTPServer, httpFactory); err != nil {
		t.Fatalf("Register(HTTP) error = %v", err)
	}
	if err := transports.Register(TypeMQTT, mqttFactory); err != nil {
		t.Fatalf("Register(MQTT) error = %v", err)
	}
	manager, err := NewManager(repository, sourceFeed, definitions, transports, WithManagerReconcileInterval(0))
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("manager.Start() error = %v", err)
	}
	defer func() {
		if stopErr := manager.Stop(context.Background()); stopErr != nil {
			t.Errorf("manager.Stop() error = %v", stopErr)
		}
	}()

	httpListener := awaitPublisherHTTPListener(t, listeners)
	mqttTransport := receiveManagerTransport(t, mqttFactory.created)
	waitPublisherStatus(t, manager, httpPublisher.ID, func(status RuntimeStatus) bool { return status.PublishCount >= 1 })
	waitPublisherStatus(t, manager, mqttPublisher.ID, func(status RuntimeStatus) bool { return status.PublishCount >= 1 })
	initialMQTTSnapshot := receiveSnapshot(t, mqttTransport.published)
	assertPublisherSnapshotValues(t, initialMQTTSnapshot, uint16(42), 12.5)

	for requestNumber := 1; requestNumber <= 3; requestNumber++ {
		response := httpRequest(t, http.MethodGet, "http://"+httpListener.Addr().String()+"/snapshot", nil, nil)
		assertHTTPResponse(t, response, http.StatusOK, true)
		var payload struct {
			Reading uint16  `json:"reading"`
			Energy  float64 `json:"energy"`
		}
		decodeHTTPBody(t, response, &payload)
		if payload.Reading != 42 || payload.Energy != 12.5 {
			t.Fatalf("HTTP payload %d = %#v", requestNumber, payload)
		}
	}
	if got := client.reads.Load(); got != 1 {
		t.Fatalf("Modbus reads after HTTP GETs = %d, want 1", got)
	}

	drainPublisherSnapshots(mqttTransport.published)
	nextBatch := sourceTestOutputBatch(instanceID, 8, readingValue.ObservedAt.Add(time.Second))
	for index := range nextBatch.Values {
		if nextBatch.Values[index].Key == "today.energy_kwh" {
			nextBatch.Values[index].Value = float64(13.5)
		}
	}
	pluginValues.publish(nextBatch)
	changedSnapshot := awaitPublisherSnapshotValue(t, mqttTransport.published, "energy", float64(13.5))
	assertPublisherSnapshotValues(t, changedSnapshot, uint16(42), 13.5)
	if got := client.reads.Load(); got != 1 {
		t.Fatalf("Modbus reads after MQTT source event = %d, want 1", got)
	}

	if err := manager.Restart(context.Background(), httpPublisher.ID); err != nil {
		t.Fatalf("Restart(HTTP) error = %v", err)
	}
	restartedHTTPListener := awaitPublisherHTTPListener(t, listeners)
	waitPublisherStatus(t, manager, httpPublisher.ID, func(status RuntimeStatus) bool { return status.PublishCount >= 1 })
	response := httpRequest(t, http.MethodGet, "http://"+restartedHTTPListener.Addr().String()+"/snapshot", nil, nil)
	assertHTTPResponse(t, response, http.StatusOK, true)
	_ = response.Body.Close()

	if err := manager.Restart(context.Background(), mqttPublisher.ID); err != nil {
		t.Fatalf("Restart(MQTT) error = %v", err)
	}
	restartedMQTTTransport := receiveManagerTransport(t, mqttFactory.created)
	restartedSnapshot := receiveSnapshot(t, restartedMQTTTransport.published)
	assertPublisherSnapshotValues(t, restartedSnapshot, uint16(42), 13.5)

	if got := client.reads.Load(); got != 1 {
		t.Fatalf("Modbus reads after Publisher restarts = %d, want 1", got)
	}
	if got := clientCreations.Load(); got != 1 {
		t.Fatalf("Modbus clients created = %d, want 1", got)
	}
	select {
	case unexpected := <-client.started:
		t.Fatalf("Publisher activity created Modbus request %#v", unexpected.request)
	case <-time.After(50 * time.Millisecond):
	}
}

func publisherAcquisitionHTTPPublisher(t *testing.T, selections []SourceSelection) Publisher {
	t.Helper()
	config := defaultHTTPPublisherConfig()
	config.Trigger = TriggerConfig{Mode: TriggerModeInterval, IntervalMS: 60_000}
	config.HTTP.Access = HTTPAccessConfig{Mode: HTTPAccessAnonymous, AnonymousAcknowledged: true}
	config.HTTP.QualityPolicy = HTTPQualityPayload
	config.Response.PayloadTemplate = `{"reading":{{value "reading"}},"energy":{{value "energy"}},"coverage":{{coverage "energy"}}}`
	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("marshal HTTP config: %v", err)
	}
	normalized, err := normalizeHTTPPublisherConfig(encoded)
	if err != nil {
		t.Fatalf("normalize HTTP config: %v", err)
	}
	return Publisher{
		ID: uuid.New(), Type: TypeHTTPServer, Name: "Acquisition proof HTTP", Enabled: true,
		Config: normalized, ConfigVersion: 4, Sources: selections, SourceCount: len(selections),
	}
}

func assertPublisherSnapshotValues(t *testing.T, snapshot SourceSnapshot, reading uint16, energy float64) {
	t.Helper()
	values := make(map[string]any, len(snapshot.Samples))
	for _, sample := range snapshot.Samples {
		values[sample.Alias] = sample.Value
	}
	if values["reading"] != reading || values["energy"] != energy {
		t.Fatalf("snapshot values = %#v, want reading=%d energy=%v", values, reading, energy)
	}
}

func awaitPublisherSnapshotValue(t *testing.T, snapshots <-chan SourceSnapshot, alias string, want any) SourceSnapshot {
	t.Helper()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	for {
		select {
		case snapshot := <-snapshots:
			for _, sample := range snapshot.Samples {
				if sample.Alias == alias && sample.Value == want {
					return snapshot
				}
			}
		case <-timer.C:
			t.Fatalf("timed out waiting for %s=%v", alias, want)
			return SourceSnapshot{}
		}
	}
}

func drainPublisherSnapshots(snapshots <-chan SourceSnapshot) {
	for {
		select {
		case <-snapshots:
		default:
			return
		}
	}
}

type publisherListenerResult struct {
	listener net.Listener
	err      error
}

func awaitPublisherHTTPListener(t *testing.T, listeners <-chan publisherListenerResult) net.Listener {
	t.Helper()
	select {
	case result := <-listeners:
		if result.err != nil {
			t.Fatalf("creating HTTP listener: %v", result.err)
		}
		return result.listener
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for HTTP listener")
		return nil
	}
}

type publisherModbusRead struct {
	request modbus.ReadRequest
	respond chan []byte
}

type publisherCountingModbusClient struct {
	connected atomic.Bool
	reads     atomic.Int32
	started   chan publisherModbusRead
}

func newPublisherCountingModbusClient() *publisherCountingModbusClient {
	return &publisherCountingModbusClient{started: make(chan publisherModbusRead, 4)}
}

func (client *publisherCountingModbusClient) Connect(context.Context) error {
	client.connected.Store(true)
	return nil
}

func (client *publisherCountingModbusClient) Disconnect() error {
	client.connected.Store(false)
	return nil
}

func (client *publisherCountingModbusClient) IsConnected() bool { return client.connected.Load() }

func (client *publisherCountingModbusClient) Read(ctx context.Context, request modbus.ReadRequest) ([]byte, error) {
	client.reads.Add(1)
	started := publisherModbusRead{request: request, respond: make(chan []byte, 1)}
	select {
	case client.started <- started:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case response := <-started.respond:
		return response, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func awaitPublisherModbusRead(t *testing.T, client *publisherCountingModbusClient) publisherModbusRead {
	t.Helper()
	select {
	case started := <-client.started:
		return started
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Modbus acquisition")
		return publisherModbusRead{}
	}
}

func awaitPublisherTagValue(t *testing.T, values *tag.MemoryValueStore, tagID uuid.UUID, want uint16) tag.TagValue {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if value, exists := values.Latest(tagID); exists && value.Value == want {
			return value
		}
		time.Sleep(time.Millisecond)
	}
	value, _ := values.Latest(tagID)
	t.Fatalf("latest Tag value = %#v, want %d", value, want)
	return tag.TagValue{}
}

type publisherAcquisitionDeviceRepository struct {
	datasource device.DatasourceContext
}

func (repository *publisherAcquisitionDeviceRepository) FindGateway(context.Context, uuid.UUID) (*device.GatewayContext, error) {
	entity := repository.datasource.Device.Gateway
	return &entity, nil
}

func (*publisherAcquisitionDeviceRepository) CreateDevice(context.Context, *device.Device) error {
	return errors.New("not implemented")
}

func (repository *publisherAcquisitionDeviceRepository) FindDevice(context.Context, uuid.UUID) (*device.DeviceContext, error) {
	entity := repository.datasource.Device
	return &entity, nil
}

func (*publisherAcquisitionDeviceRepository) ListDevices(context.Context, uuid.UUID) ([]device.DeviceView, error) {
	return nil, errors.New("not implemented")
}

func (*publisherAcquisitionDeviceRepository) ListDeviceInventory(context.Context, device.DeviceInventoryInput) (*device.DeviceInventoryResult, error) {
	return nil, errors.New("not implemented")
}

func (*publisherAcquisitionDeviceRepository) UpdateDevice(context.Context, *device.Device) error {
	return errors.New("not implemented")
}

func (*publisherAcquisitionDeviceRepository) DeleteDevice(context.Context, uuid.UUID) error {
	return errors.New("not implemented")
}

func (*publisherAcquisitionDeviceRepository) CreateDatasource(context.Context, *device.Datasource) error {
	return errors.New("not implemented")
}

func (repository *publisherAcquisitionDeviceRepository) FindDatasource(context.Context, uuid.UUID) (*device.DatasourceContext, error) {
	entity := repository.datasource
	return &entity, nil
}

func (*publisherAcquisitionDeviceRepository) ListDatasources(context.Context, uuid.UUID) ([]device.Datasource, error) {
	return nil, errors.New("not implemented")
}

func (*publisherAcquisitionDeviceRepository) UpdateDatasource(context.Context, *device.Datasource) error {
	return errors.New("not implemented")
}

func (*publisherAcquisitionDeviceRepository) DeleteDatasource(context.Context, uuid.UUID) error {
	return errors.New("not implemented")
}

type publisherAcquisitionTagRepository struct {
	entity tag.Tag
}

func (*publisherAcquisitionTagRepository) Create(context.Context, *tag.Tag, []uuid.UUID) error {
	return errors.New("not implemented")
}

func (repository *publisherAcquisitionTagRepository) Find(_ context.Context, id uuid.UUID) (*tag.Tag, error) {
	if id != repository.entity.ID {
		return nil, tag.ErrTagNotFound
	}
	entity := repository.entity
	return &entity, nil
}

func (repository *publisherAcquisitionTagRepository) Get(ctx context.Context, id uuid.UUID) (*tag.Tag, error) {
	return repository.Find(ctx, id)
}

func (repository *publisherAcquisitionTagRepository) List(context.Context, tag.ListInput) (*tag.ListResult, error) {
	return &tag.ListResult{Data: []tag.Tag{repository.entity}, Page: 1, PerPage: 100, Total: 1, TotalPages: 1}, nil
}

func (*publisherAcquisitionTagRepository) Update(context.Context, *tag.Tag, []uuid.UUID) error {
	return errors.New("not implemented")
}

func (*publisherAcquisitionTagRepository) Delete(context.Context, uuid.UUID) error {
	return errors.New("not implemented")
}

func (*publisherAcquisitionTagRepository) ListDependencies(context.Context) ([]tag.Dependency, error) {
	return []tag.Dependency{}, nil
}

func (repository *publisherAcquisitionTagRepository) ListEnabledReadingTags(context.Context) ([]tag.Tag, error) {
	return []tag.Tag{repository.entity}, nil
}

func (repository *publisherAcquisitionTagRepository) ListEnabledTags(context.Context) ([]tag.Tag, error) {
	return []tag.Tag{repository.entity}, nil
}
