package device

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/protocol"
)

func TestServiceCreatesProtocolNeutralDeviceAndDatasource(t *testing.T) {
	t.Parallel()
	repository := newMemoryRepository()
	driver := &testDatasourceDriver{}
	service := newTestService(t, repository, driver)

	createdDevice, err := service.CreateDevice(context.Background(), repository.gateway.ID, CreateDeviceInput{Name: "  Meter  ", Type: DeviceTypeModbus, Config: json.RawMessage(`{"unit_id":7}`)})
	if err != nil {
		t.Fatalf("CreateDevice() error = %v", err)
	}
	if createdDevice.Name != "Meter" || string(createdDevice.Config) != `{"normalized":true,"unit_id":7}` {
		t.Errorf("device = %#v", createdDevice)
	}

	createdDatasource, err := service.CreateDatasource(context.Background(), createdDevice.ID, CreateDatasourceInput{Name: "  Voltage  ", Type: DatasourceTypeModbusRead, Config: json.RawMessage(`{"address":10}`)})
	if err != nil {
		t.Fatalf("CreateDatasource() error = %v", err)
	}
	if createdDatasource.Name != "Voltage" || string(createdDatasource.Config) != `{"address":10,"normalized":true}` {
		t.Errorf("datasource = %#v", createdDatasource)
	}
	if driver.deviceNormalizations.Load() != 1 || driver.datasourceNormalizations.Load() != 1 {
		t.Errorf("normalization calls = (%d,%d)", driver.deviceNormalizations.Load(), driver.datasourceNormalizations.Load())
	}
}

func TestServiceRejectsCrossProtocolDatasource(t *testing.T) {
	t.Parallel()
	repository := newMemoryRepository()
	repository.gateway.Type = "mqtt"
	service := newTestService(t, repository, &testDatasourceDriver{})
	_, err := service.CreateDevice(context.Background(), repository.gateway.ID, CreateDeviceInput{Name: "Meter", Type: DeviceTypeModbus, Config: json.RawMessage(`{"unit_id":1}`)})
	if !errors.Is(err, ErrProtocolMismatch) {
		t.Fatalf("error = %v, want protocol mismatch", err)
	}
}

func TestServiceListsGlobalDeviceInventoryWithValidatedDefaults(t *testing.T) {
	t.Parallel()
	repository := newMemoryRepository()
	repository.addDevice()
	service := newTestService(t, repository, &testDatasourceDriver{})
	deviceType := DeviceTypeModbus
	enabled := true

	result, err := service.ListDeviceInventory(context.Background(), DeviceInventoryInput{
		VGatewayID: &repository.gateway.ID,
		Type:       &deviceType,
		Enabled:    &enabled,
		Search:     "Meter",
	})
	if err != nil {
		t.Fatalf("ListDeviceInventory() error = %v", err)
	}
	if len(result.Data) != 1 || result.Page != 1 || result.PerPage != defaultDevicesPerPage {
		t.Fatalf("inventory result = %#v", result)
	}
	if repository.inventoryInput.Page != 1 || repository.inventoryInput.PerPage != defaultDevicesPerPage || repository.inventoryInput.Search != "Meter" {
		t.Errorf("repository input = %#v", repository.inventoryInput)
	}
}

func TestServiceRejectsInvalidDeviceInventoryFilters(t *testing.T) {
	t.Parallel()
	service := newTestService(t, newMemoryRepository(), &testDatasourceDriver{})
	unsupported := DeviceType("mqtt_device")
	tests := []struct {
		name  string
		input DeviceInventoryInput
		want  error
	}{
		{name: "unsupported type", input: DeviceInventoryInput{Type: &unsupported}, want: ErrUnsupportedDeviceType},
		{name: "negative page", input: DeviceInventoryInput{Page: -1}, want: ErrInvalidDevice},
		{name: "negative page size", input: DeviceInventoryInput{PerPage: -1}, want: ErrInvalidDevice},
		{name: "oversized page", input: DeviceInventoryInput{PerPage: maxDevicesPerPage + 1}, want: ErrInvalidDevice},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := service.ListDeviceInventory(context.Background(), test.input)
			if !errors.Is(err, test.want) {
				t.Fatalf("ListDeviceInventory() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestServicePreviewDoesNotPersistAndFormatsSample(t *testing.T) {
	t.Parallel()
	repository := newMemoryRepository()
	service := newTestService(t, repository, &testDatasourceDriver{})
	parent := repository.addDevice()

	sample, err := service.PreviewDatasource(context.Background(), parent.ID, PreviewDatasourceInput{Type: DatasourceTypeModbusRead, Config: json.RawMessage(`{"address":1}`)})
	if err != nil {
		t.Fatalf("PreviewDatasource() error = %v", err)
	}
	if len(repository.datasources) != 0 {
		t.Fatalf("datasources persisted = %d, want 0", len(repository.datasources))
	}
	if sample.DatasourceID != uuid.Nil || sample.RawHex != "1234" || sample.Sequence == 0 || sample.Quality != "good" {
		t.Errorf("sample = %#v", sample)
	}
}

func TestServiceRecordsPreviewAndMonitorGatewayRequests(t *testing.T) {
	repository := newMemoryRepository()
	parent := repository.addDevice()
	datasource := repository.addDatasource(parent.ID)
	driver := &testDatasourceDriver{monitorStarted: make(chan struct{})}
	recorder := &testGatewayRequestRecorder{requests: make(chan recordedGatewayRequest, 4)}
	service, err := NewServiceWithGatewayRequestRecorder(repository, recorder, driver)
	if err != nil {
		t.Fatalf("NewServiceWithGatewayRequestRecorder() error = %v", err)
	}

	if _, err := service.PreviewSavedDatasource(context.Background(), datasource.ID); err != nil {
		t.Fatalf("PreviewSavedDatasource() error = %v", err)
	}
	preview := <-recorder.requests
	if preview.gatewayID != repository.gateway.ID || preview.bytesReceived != 2 || preview.latency != 2*time.Millisecond || preview.err != nil {
		t.Fatalf("preview request = %#v", preview)
	}

	stream, unsubscribe, err := service.Subscribe(context.Background(), datasource.ID)
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	defer unsubscribe()
	select {
	case <-stream:
	case <-time.After(time.Second):
		t.Fatal("monitor emitted no sample")
	}
	monitor := <-recorder.requests
	if monitor.gatewayID != repository.gateway.ID || monitor.bytesReceived != 2 || monitor.err != nil {
		t.Fatalf("monitor request = %#v", monitor)
	}
	service.stopMonitor(datasource.ID)
}

func TestServiceRecordsFailedGatewayRequest(t *testing.T) {
	repository := newMemoryRepository()
	parent := repository.addDevice()
	driverError := errors.New("driver read failed")
	recorder := &testGatewayRequestRecorder{requests: make(chan recordedGatewayRequest, 1)}
	service, err := NewServiceWithGatewayRequestRecorder(
		repository,
		recorder,
		&testDatasourceDriver{previewErr: driverError},
	)
	if err != nil {
		t.Fatalf("NewServiceWithGatewayRequestRecorder() error = %v", err)
	}

	_, err = service.PreviewDatasource(context.Background(), parent.ID, PreviewDatasourceInput{
		Type:   DatasourceTypeModbusRead,
		Config: json.RawMessage(`{"address":1}`),
	})
	if !errors.Is(err, driverError) {
		t.Fatalf("PreviewDatasource() error = %v", err)
	}
	recorded := <-recorder.requests
	if recorded.gatewayID != repository.gateway.ID || recorded.err != driverError || recorded.bytesReceived != 0 || recorded.latency <= 0 || recorded.observedAt.IsZero() {
		t.Fatalf("failed request = %#v", recorded)
	}
}

func TestServiceSharesOneMonitorAndFansOutSamples(t *testing.T) {
	repository := newMemoryRepository()
	parent := repository.addDevice()
	datasource := repository.addDatasource(parent.ID)
	driver := &testDatasourceDriver{monitorStarted: make(chan struct{})}
	service := newTestService(t, repository, driver)

	first, unsubscribeFirst, err := service.Subscribe(context.Background(), datasource.ID)
	if err != nil {
		t.Fatalf("first Subscribe() error = %v", err)
	}
	defer unsubscribeFirst()
	select {
	case <-driver.monitorStarted:
	case <-time.After(time.Second):
		t.Fatal("monitor did not start")
	}
	second, unsubscribeSecond, err := service.Subscribe(context.Background(), datasource.ID)
	if err != nil {
		t.Fatalf("second Subscribe() error = %v", err)
	}
	defer unsubscribeSecond()

	for index, stream := range []<-chan DatasourceSample{first, second} {
		select {
		case sample := <-stream:
			if sample.RawHex != "1234" {
				t.Errorf("subscriber %d sample = %#v", index, sample)
			}
		case <-time.After(time.Second):
			t.Fatalf("subscriber %d received no sample", index)
		}
	}
	if driver.monitorCalls.Load() != 1 {
		t.Errorf("Monitor() calls = %d, want 1", driver.monitorCalls.Load())
	}
	latest, err := service.LatestSample(context.Background(), datasource.ID)
	if err != nil {
		t.Fatalf("LatestSample() error = %v", err)
	}
	if latest.RawHex != "1234" || latest.DatasourceID != datasource.ID {
		t.Errorf("latest sample = %#v", latest)
	}
	service.stopMonitor(datasource.ID)
	latest, err = service.LatestSample(context.Background(), datasource.ID)
	if err != nil {
		t.Fatalf("LatestSample() after monitor stop error = %v", err)
	}
	if latest.RawHex != "1234" {
		t.Errorf("latest sample after monitor stop = %#v", latest)
	}
}

func TestServiceAdaptsMonitoredSamplesForTagRuntime(t *testing.T) {
	repository := newMemoryRepository()
	parent := repository.addDevice()
	datasource := repository.addDatasource(parent.ID)
	driver := &testDatasourceDriver{monitorStarted: make(chan struct{})}
	service := newTestService(t, repository, driver)

	stream, unsubscribe, err := service.SubscribeDatasourceForTags(context.Background(), datasource.ID)
	if err != nil {
		t.Fatalf("SubscribeDatasourceForTags() error = %v", err)
	}
	defer unsubscribe()
	select {
	case sample := <-stream:
		if sample.Quality != "good" || string(sample.Raw) != string([]byte{0x12, 0x34}) || string(sample.Data) != `{}` {
			t.Errorf("sample = %#v", sample)
		}
	case <-time.After(time.Second):
		t.Fatal("Tag runtime received no sample")
	}
	if driver.monitorCalls.Load() != 1 {
		t.Errorf("Monitor() calls = %d, want 1", driver.monitorCalls.Load())
	}
}

func TestServiceLatestSampleRequiresAnActiveSample(t *testing.T) {
	t.Parallel()
	repository := newMemoryRepository()
	parent := repository.addDevice()
	datasource := repository.addDatasource(parent.ID)
	service := newTestService(t, repository, &testDatasourceDriver{})
	if _, err := service.LatestSample(context.Background(), datasource.ID); !errors.Is(err, ErrDatasourceSampleUnavailable) {
		t.Fatalf("LatestSample() error = %v", err)
	}
}

func TestServiceReportsPausedAndRejectsMonitoringWhenHierarchyIsDisabled(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		disable func(*memoryRepository, Device, Datasource)
	}{
		{name: "gateway", disable: func(repository *memoryRepository, _ Device, _ Datasource) { repository.gateway.Enabled = false }},
		{name: "device", disable: func(repository *memoryRepository, parent Device, _ Datasource) {
			parent.Enabled = false
			repository.devices[parent.ID] = parent
		}},
		{name: "datasource", disable: func(repository *memoryRepository, _ Device, datasource Datasource) {
			datasource.Enabled = false
			repository.datasources[datasource.ID] = datasource
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := newMemoryRepository()
			parent := repository.addDevice()
			datasource := repository.addDatasource(parent.ID)
			test.disable(repository, parent, datasource)
			service := newTestService(t, repository, &testDatasourceDriver{})

			view, err := service.GetDatasource(context.Background(), datasource.ID)
			if err != nil {
				t.Fatalf("GetDatasource() error = %v", err)
			}
			if view.Status != "paused" {
				t.Errorf("GetDatasource() status = %q, want paused", view.Status)
			}
			views, err := service.ListDatasources(context.Background(), parent.ID)
			if err != nil {
				t.Fatalf("ListDatasources() error = %v", err)
			}
			if len(views) != 1 || views[0].Status != "paused" {
				t.Errorf("ListDatasources() = %#v", views)
			}
			if _, _, err := service.Subscribe(context.Background(), datasource.ID); !errors.Is(err, ErrMonitoringDisabled) {
				t.Fatalf("Subscribe() error = %v, want monitoring disabled", err)
			}
		})
	}
}

func TestServiceTagReadRequiresEnabledHierarchyWhileManualPreviewRemainsAvailable(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		disable func(*memoryRepository, Device, Datasource)
	}{
		{name: "gateway", disable: func(repository *memoryRepository, _ Device, _ Datasource) { repository.gateway.Enabled = false }},
		{name: "device", disable: func(repository *memoryRepository, parent Device, _ Datasource) {
			parent.Enabled = false
			repository.devices[parent.ID] = parent
		}},
		{name: "datasource", disable: func(repository *memoryRepository, _ Device, datasource Datasource) {
			datasource.Enabled = false
			repository.datasources[datasource.ID] = datasource
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := newMemoryRepository()
			parent := repository.addDevice()
			datasource := repository.addDatasource(parent.ID)
			test.disable(repository, parent, datasource)
			driver := &testDatasourceDriver{}
			service := newTestService(t, repository, driver)

			if _, err := service.ReadDatasourceForTag(context.Background(), datasource.ID); !errors.Is(err, ErrMonitoringDisabled) {
				t.Fatalf("ReadDatasourceForTag() error = %v, want monitoring disabled", err)
			}
			if driver.previewCalls.Load() != 0 {
				t.Fatalf("Preview() calls after rejected Tag read = %d", driver.previewCalls.Load())
			}
			if _, err := service.PreviewSavedDatasource(context.Background(), datasource.ID); err != nil {
				t.Fatalf("PreviewSavedDatasource() error = %v", err)
			}
			if driver.previewCalls.Load() != 1 {
				t.Errorf("Preview() calls after manual preview = %d, want 1", driver.previewCalls.Load())
			}
		})
	}
}

func TestServiceDatasourceReadsPreserveDriverError(t *testing.T) {
	t.Parallel()
	repository := newMemoryRepository()
	parent := repository.addDevice()
	datasource := repository.addDatasource(parent.ID)
	driverError := errors.New("protocol timeout")
	driver := &testDatasourceDriver{previewErr: driverError}
	service := newTestService(t, repository, driver)

	if _, err := service.ReadDatasourceForTag(context.Background(), datasource.ID); !errors.Is(err, ErrDatasourceReadFailed) || !errors.Is(err, driverError) {
		t.Fatalf("ReadDatasourceForTag() error = %v", err)
	}
	if _, err := service.PreviewSavedDatasource(context.Background(), datasource.ID); !errors.Is(err, ErrDatasourceReadFailed) || !errors.Is(err, driverError) {
		t.Fatalf("PreviewSavedDatasource() error = %v", err)
	}
}

func TestServiceRetainsLastPayloadWhenMonitorReportsBadQuality(t *testing.T) {
	t.Parallel()
	repository := newMemoryRepository()
	parent := repository.addDevice()
	datasource := repository.addDatasource(parent.ID)
	service := newTestService(t, repository, &testDatasourceDriver{})
	service.storeSample(DatasourceSample{DatasourceID: datasource.ID, Quality: "good", RawHex: "1234", Data: json.RawMessage(`{"registers":[{"value":4660}]}`)})
	service.storeSample(DatasourceSample{DatasourceID: datasource.ID, Quality: "bad", Error: "Read failed"})

	latest, err := service.LatestSample(context.Background(), datasource.ID)
	if err != nil {
		t.Fatalf("LatestSample() error = %v", err)
	}
	if latest.Quality != "bad" || latest.Error != "Read failed" || latest.RawHex != "1234" || string(latest.Data) != `{"registers":[{"value":4660}]}` {
		t.Errorf("latest sample = %#v", latest)
	}
}

func TestServicePreservesLatestSampleWhenDatasourceMutationFails(t *testing.T) {
	t.Parallel()
	failure := errors.New("database unavailable")

	tests := []struct {
		name   string
		mutate func(*Service, uuid.UUID) error
		repo   func(*memoryRepository) Repository
	}{
		{
			name: "update",
			mutate: func(service *Service, id uuid.UUID) error {
				_, err := service.UpdateDatasource(context.Background(), id, UpdateDatasourceInput{Name: stringPointer("Updated")})
				return err
			},
			repo: func(repository *memoryRepository) Repository {
				return &failingRepository{memoryRepository: repository, updateDatasourceErr: failure}
			},
		},
		{
			name: "delete",
			mutate: func(service *Service, id uuid.UUID) error {
				return service.DeleteDatasource(context.Background(), id)
			},
			repo: func(repository *memoryRepository) Repository {
				return &failingRepository{memoryRepository: repository, deleteDatasourceErr: failure}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := newMemoryRepository()
			parent := repository.addDevice()
			datasource := repository.addDatasource(parent.ID)
			service := newTestService(t, test.repo(repository), &testDatasourceDriver{})
			service.storeSample(DatasourceSample{DatasourceID: datasource.ID, Quality: "good", RawHex: "1234"})

			if err := test.mutate(service, datasource.ID); !errors.Is(err, failure) {
				t.Fatalf("mutation error = %v, want %v", err, failure)
			}
			latest, err := service.LatestSample(context.Background(), datasource.ID)
			if err != nil {
				t.Fatalf("LatestSample() error = %v", err)
			}
			if latest.RawHex != "1234" {
				t.Errorf("latest sample = %#v", latest)
			}
		})
	}
}

func TestServiceRejectsUnsupportedStoredDeviceTypeOnConfigUpdate(t *testing.T) {
	t.Parallel()
	repository := newMemoryRepository()
	entity := repository.addDevice()
	entity.Type = "unsupported"
	repository.devices[entity.ID] = entity
	service := newTestService(t, repository, &testDatasourceDriver{})
	config := Config(`{"unit_id":2}`)

	_, err := service.UpdateDevice(context.Background(), entity.ID, UpdateDeviceInput{Config: &config})
	if !errors.Is(err, ErrUnsupportedDeviceType) {
		t.Fatalf("UpdateDevice() error = %v, want unsupported type", err)
	}
}

func TestExecutionGateSerializesOperationsAndHonorsCancellation(t *testing.T) {
	t.Parallel()
	gate := newExecutionGate()
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- gate.Execute(context.Background(), func(context.Context) error {
			close(firstEntered)
			<-releaseFirst
			return nil
		})
	}()
	<-firstEntered

	secondCalled := atomic.Bool{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := gate.Execute(ctx, func(context.Context) error {
		secondCalled.Store(true)
		return nil
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("second Execute() error = %v, want context canceled", err)
	}
	if secondCalled.Load() {
		t.Fatal("second operation ran while gate was occupied")
	}
	close(releaseFirst)
	if err := <-firstDone; err != nil {
		t.Fatalf("first Execute() error = %v", err)
	}
}

func TestServiceReusesExecutionGatePerGateway(t *testing.T) {
	t.Parallel()
	service := newTestService(t, newMemoryRepository(), &testDatasourceDriver{})
	firstGateway := uuid.New()
	secondGateway := uuid.New()
	if service.gatewayGate(firstGateway) != service.gatewayGate(firstGateway) {
		t.Fatal("same gateway received different execution gates")
	}
	if service.gatewayGate(firstGateway) == service.gatewayGate(secondGateway) {
		t.Fatal("different gateways shared one execution gate")
	}
}

type testDatasourceDriver struct {
	deviceNormalizations     atomic.Int64
	datasourceNormalizations atomic.Int64
	previewCalls             atomic.Int64
	previewErr               error
	monitorCalls             atomic.Int64
	monitorStarted           chan struct{}
}

type recordedGatewayRequest struct {
	gatewayID     uuid.UUID
	observedAt    time.Time
	latency       time.Duration
	bytesReceived int
	err           error
}

type testGatewayRequestRecorder struct {
	requests chan recordedGatewayRequest
}

func (r *testGatewayRequestRecorder) RecordGatewayRequest(
	gatewayID uuid.UUID,
	observedAt time.Time,
	latency time.Duration,
	bytesReceived int,
	err error,
) {
	r.requests <- recordedGatewayRequest{
		gatewayID:     gatewayID,
		observedAt:    observedAt,
		latency:       latency,
		bytesReceived: bytesReceived,
		err:           err,
	}
}

func (*testDatasourceDriver) GatewayType() string { return "modbus_tcp" }
func (*testDatasourceDriver) DeviceType() string  { return string(DeviceTypeModbus) }
func (*testDatasourceDriver) DatasourceTypes() []string {
	return []string{string(DatasourceTypeModbusRead)}
}
func (d *testDatasourceDriver) NormalizeDeviceConfig(raw json.RawMessage) (json.RawMessage, error) {
	d.deviceNormalizations.Add(1)
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	value["normalized"] = true
	return json.Marshal(value)
}
func (d *testDatasourceDriver) NormalizeDatasourceConfig(_ string, raw json.RawMessage) (json.RawMessage, error) {
	d.datasourceNormalizations.Add(1)
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	value["normalized"] = true
	return json.Marshal(value)
}
func (d *testDatasourceDriver) Preview(context.Context, protocol.DatasourceReadRequest) (protocol.DatasourceSample, error) {
	d.previewCalls.Add(1)
	if d.previewErr != nil {
		return protocol.DatasourceSample{}, d.previewErr
	}
	return protocol.DatasourceSample{ObservedAt: time.Now().UTC(), Latency: 2 * time.Millisecond, Quality: "good", Raw: []byte{0x12, 0x34}, Data: json.RawMessage(`{"registers":[]}`)}, nil
}
func (d *testDatasourceDriver) Monitor(ctx context.Context, _ protocol.DatasourceReadRequest, _ time.Duration, emit protocol.SampleEmitter) error {
	d.monitorCalls.Add(1)
	if d.monitorStarted != nil {
		close(d.monitorStarted)
	}
	emit(protocol.DatasourceSample{ObservedAt: time.Now().UTC(), Quality: "good", Raw: []byte{0x12, 0x34}, Data: json.RawMessage(`{}`)})
	<-ctx.Done()
	return ctx.Err()
}

type memoryRepository struct {
	gateway        GatewayContext
	devices        map[uuid.UUID]Device
	datasources    map[uuid.UUID]Datasource
	inventoryInput DeviceInventoryInput
}

type failingRepository struct {
	*memoryRepository
	updateDatasourceErr error
	deleteDatasourceErr error
}

func (r *failingRepository) UpdateDatasource(ctx context.Context, entity *Datasource) error {
	if r.updateDatasourceErr != nil {
		return r.updateDatasourceErr
	}
	return r.memoryRepository.UpdateDatasource(ctx, entity)
}

func (r *failingRepository) DeleteDatasource(ctx context.Context, id uuid.UUID) error {
	if r.deleteDatasourceErr != nil {
		return r.deleteDatasourceErr
	}
	return r.memoryRepository.DeleteDatasource(ctx, id)
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{gateway: GatewayContext{ID: uuid.New(), Type: "modbus_tcp", Enabled: true, Config: json.RawMessage(`{"host":"plc"}`)}, devices: map[uuid.UUID]Device{}, datasources: map[uuid.UUID]Datasource{}}
}
func (r *memoryRepository) addDevice() Device {
	entity := Device{ID: uuid.New(), VGatewayID: r.gateway.ID, Name: "Meter", Type: DeviceTypeModbus, Enabled: true, Config: json.RawMessage(`{"unit_id":1,"poll_interval_ms":100}`)}
	r.devices[entity.ID] = entity
	return entity
}
func (r *memoryRepository) addDatasource(deviceID uuid.UUID) Datasource {
	entity := Datasource{ID: uuid.New(), DeviceID: deviceID, Name: "Registers", Type: DatasourceTypeModbusRead, Enabled: true, Config: json.RawMessage(`{"poll_interval_ms":100}`)}
	r.datasources[entity.ID] = entity
	return entity
}
func (r *memoryRepository) FindGateway(_ context.Context, id uuid.UUID) (*GatewayContext, error) {
	if id != r.gateway.ID {
		return nil, ErrGatewayNotFound
	}
	value := r.gateway
	return &value, nil
}
func (r *memoryRepository) CreateDevice(_ context.Context, entity *Device) error {
	entity.ID = uuid.New()
	entity.CreatedAt = time.Now()
	entity.UpdatedAt = entity.CreatedAt
	r.devices[entity.ID] = *entity
	return nil
}
func (r *memoryRepository) FindDevice(_ context.Context, id uuid.UUID) (*DeviceContext, error) {
	entity, ok := r.devices[id]
	if !ok {
		return nil, ErrDeviceNotFound
	}
	return &DeviceContext{Device: entity, Gateway: r.gateway}, nil
}
func (r *memoryRepository) ListDevices(_ context.Context, gatewayID uuid.UUID) ([]DeviceView, error) {
	result := []DeviceView{}
	for _, entity := range r.devices {
		if entity.VGatewayID == gatewayID {
			result = append(result, DeviceView{Device: entity})
		}
	}
	return result, nil
}
func (r *memoryRepository) ListDeviceInventory(_ context.Context, input DeviceInventoryInput) (*DeviceInventoryResult, error) {
	r.inventoryInput = input
	result := make([]DeviceInventoryItem, 0)
	for _, entity := range r.devices {
		if input.VGatewayID != nil && entity.VGatewayID != *input.VGatewayID {
			continue
		}
		if input.Type != nil && entity.Type != *input.Type {
			continue
		}
		if input.Enabled != nil && entity.Enabled != *input.Enabled {
			continue
		}
		result = append(result, DeviceInventoryItem{Device: entity, VGatewayName: "Gateway", VGatewayType: r.gateway.Type, VGatewayEnabled: r.gateway.Enabled})
	}
	return &DeviceInventoryResult{Data: result, Page: input.Page, PerPage: input.PerPage, Total: int64(len(result)), TotalPages: 1}, nil
}
func (r *memoryRepository) UpdateDevice(_ context.Context, entity *Device) error {
	if _, ok := r.devices[entity.ID]; !ok {
		return ErrDeviceNotFound
	}
	r.devices[entity.ID] = *entity
	return nil
}
func (r *memoryRepository) DeleteDevice(_ context.Context, id uuid.UUID) error {
	if _, ok := r.devices[id]; !ok {
		return ErrDeviceNotFound
	}
	delete(r.devices, id)
	for key, datasource := range r.datasources {
		if datasource.DeviceID == id {
			delete(r.datasources, key)
		}
	}
	return nil
}
func (r *memoryRepository) CreateDatasource(_ context.Context, entity *Datasource) error {
	entity.ID = uuid.New()
	entity.CreatedAt = time.Now()
	entity.UpdatedAt = entity.CreatedAt
	r.datasources[entity.ID] = *entity
	return nil
}
func (r *memoryRepository) FindDatasource(_ context.Context, id uuid.UUID) (*DatasourceContext, error) {
	entity, ok := r.datasources[id]
	if !ok {
		return nil, ErrDatasourceNotFound
	}
	parent, err := r.FindDevice(context.Background(), entity.DeviceID)
	if err != nil {
		return nil, err
	}
	return &DatasourceContext{Datasource: entity, Device: *parent}, nil
}
func (r *memoryRepository) ListDatasources(_ context.Context, deviceID uuid.UUID) ([]Datasource, error) {
	result := []Datasource{}
	for _, entity := range r.datasources {
		if entity.DeviceID == deviceID {
			result = append(result, entity)
		}
	}
	return result, nil
}
func (r *memoryRepository) UpdateDatasource(_ context.Context, entity *Datasource) error {
	if _, ok := r.datasources[entity.ID]; !ok {
		return ErrDatasourceNotFound
	}
	r.datasources[entity.ID] = *entity
	return nil
}
func (r *memoryRepository) DeleteDatasource(_ context.Context, id uuid.UUID) error {
	if _, ok := r.datasources[id]; !ok {
		return ErrDatasourceNotFound
	}
	delete(r.datasources, id)
	return nil
}

func newTestService(t *testing.T, repository Repository, driver protocol.DatasourceDriver) *Service {
	t.Helper()
	service, err := NewService(repository, driver)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return service
}

func stringPointer(value string) *string { return &value }
