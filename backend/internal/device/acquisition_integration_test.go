package device

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/protocol/modbus"
	"github.com/thefuriousowl/iot-edge/internal/tag"
)

func TestAcquisitionRuntimeFansEachModbusPollOutToAllReadingTags(t *testing.T) {
	repository := newMemoryRepository()
	repository.gateway.Config = json.RawMessage(`{"host":"plc.local","port":502,"timeout":5000,"retry_count":0,"retry_delay":0,"keep_alive":true,"reconnect_interval":0}`)
	parent := repository.addDevice()
	parent.Config = json.RawMessage(`{"unit_id":7,"request_timeout_ms":null}`)
	repository.devices[parent.ID] = parent
	datasource := repository.addDatasource(parent.ID)
	datasource.Config = json.RawMessage(`{"function_code":3,"start_address":0,"quantity":2,"poll_interval_ms":100}`)
	repository.datasources[datasource.ID] = datasource

	client := newControlledModbusClient()
	var clientCreations atomic.Int32
	driver, err := modbus.NewModbusTCPDriver(func(modbus.ModbusTCPConfig) (modbus.ModbusClient, error) {
		clientCreations.Add(1)
		return client, nil
	})
	if err != nil {
		t.Fatalf("NewModbusTCPDriver() error = %v", err)
	}
	deviceService, err := NewService(repository, driver)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	tagRepository := newAcquisitionTagRepository(datasource.ID)
	tagService, err := tag.NewService(tagRepository, deviceService, tag.NewBinaryNumericDecoder())
	if err != nil {
		t.Fatalf("tag.NewService() error = %v", err)
	}
	values := tag.NewMemoryValueStore()
	runtime, err := tag.NewAcquisitionRuntime(
		tagRepository,
		tagService,
		deviceService,
		values,
		tag.WithAcquisitionReconcileInterval(0),
	)
	if err != nil {
		t.Fatalf("tag.NewAcquisitionRuntime() error = %v", err)
	}
	if err := runtime.Start(t.Context()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() {
		runtime.Stop()
		deviceService.stopMonitor(datasource.ID)
	})

	var previousRelease time.Time
	for poll := 1; poll <= 3; poll++ {
		started := awaitModbusRead(t, client)
		if started.request != (modbus.ReadRequest{UnitID: 7, FunctionCode: modbus.FunctionReadHoldingRegisters, Address: 0, Quantity: 2}) {
			t.Fatalf("poll %d request = %#v", poll, started.request)
		}
		if poll > 1 && started.at.Sub(previousRelease) < 80*time.Millisecond {
			t.Fatalf("poll %d started after %s, want 100ms polling without duplicate reads", poll, started.at.Sub(previousRelease))
		}
		previousRelease = time.Now()
		client.release <- struct{}{}
		awaitTagHistoryLength(t, values, tagRepository.tags, poll)
	}

	runtime.Stop()
	deviceService.stopMonitor(datasource.ID)
	if got := client.reads.Load(); got != 3 {
		t.Fatalf("Modbus Read() calls = %d, want exactly one request per poll", got)
	}
	if got := clientCreations.Load(); got != 1 {
		t.Fatalf("Modbus clients created = %d, want one shared Datasource monitor", got)
	}
	assertAcquiredTagHistory(t, values, tagRepository.tags[0], []any{uint16(1), uint16(2), uint16(3)})
	assertAcquiredTagHistory(t, values, tagRepository.tags[1], []any{uint16(11), uint16(12), uint16(13)})
	assertAcquiredTagHistory(t, values, tagRepository.tags[2], []any{uint32(65547), uint32(131084), uint32(196621)})
	for poll := 0; poll < 3; poll++ {
		observedAt := values.History(tagRepository.tags[0].ID, 10)[poll].ObservedAt
		for _, entity := range tagRepository.tags[1:] {
			if got := values.History(entity.ID, 10)[poll].ObservedAt; !got.Equal(observedAt) {
				t.Errorf("poll %d Tag %s observed_at = %s, want %s", poll+1, entity.ID, got, observedAt)
			}
		}
	}
}

func TestAcquisitionRuntimeCalculatesFromCrossDatasourceSnapshotWithoutExtraReads(t *testing.T) {
	repository := newMemoryRepository()
	repository.gateway.Config = json.RawMessage(`{"host":"plc.local","port":502,"timeout":5000,"retry_count":0,"retry_delay":0,"keep_alive":true,"reconnect_interval":0}`)
	parent := repository.addDevice()
	parent.Config = json.RawMessage(`{"unit_id":7,"request_timeout_ms":null}`)
	repository.devices[parent.ID] = parent
	fastDatasource := repository.addDatasource(parent.ID)
	fastDatasource.Name = "One second registers"
	fastDatasource.Config = json.RawMessage(`{"function_code":3,"start_address":0,"quantity":1,"poll_interval_ms":1000}`)
	repository.datasources[fastDatasource.ID] = fastDatasource
	slowDatasource := repository.addDatasource(parent.ID)
	slowDatasource.Name = "Ten second registers"
	slowDatasource.Config = json.RawMessage(`{"function_code":3,"start_address":100,"quantity":1,"poll_interval_ms":10000}`)
	repository.datasources[slowDatasource.ID] = slowDatasource

	hub := newRoutedModbusHub()
	driver, err := modbus.NewModbusTCPDriver(func(modbus.ModbusTCPConfig) (modbus.ModbusClient, error) {
		hub.clientCreations.Add(1)
		return &routedModbusClient{hub: hub}, nil
	})
	if err != nil {
		t.Fatalf("NewModbusTCPDriver() error = %v", err)
	}
	deviceService, err := NewService(repository, driver)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	fastTag := tag.Tag{ID: uuid.New(), DatasourceID: &fastDatasource.ID, Name: "Fast reading", Type: tag.TypeReading, DataType: tag.DataTypeUInt16, Enabled: true, Config: json.RawMessage(`{"decoder":{"type":"binary_numeric","config":{"byte_offset":0}}}`)}
	slowTag := tag.Tag{ID: uuid.New(), DatasourceID: &slowDatasource.ID, Name: "Slow reading", Type: tag.TypeReading, DataType: tag.DataTypeUInt16, Enabled: true, Config: json.RawMessage(`{"decoder":{"type":"binary_numeric","config":{"byte_offset":0}}}`)}
	calculatedTag := tag.Tag{
		ID:       uuid.New(),
		Name:     "Fast multiplied by latest slow",
		Type:     tag.TypeCalculated,
		DataType: tag.DataTypeUInt32,
		Enabled:  true,
		Config: json.RawMessage(fmt.Sprintf(
			`{"expression":"${%s} * ${%s}","trigger":{"tag_id":"%s","mode":"on_sample"}}`,
			fastTag.ID,
			slowTag.ID,
			fastTag.ID,
		)),
	}
	tagRepository := &acquisitionTagRepository{tags: []tag.Tag{fastTag, slowTag, calculatedTag}}
	tagService, err := tag.NewService(tagRepository, deviceService, tag.NewBinaryNumericDecoder())
	if err != nil {
		t.Fatalf("tag.NewService() error = %v", err)
	}
	values := tag.NewMemoryValueStore()
	runtime, err := tag.NewAcquisitionRuntime(
		tagRepository,
		tagService,
		deviceService,
		values,
		tag.WithAcquisitionReconcileInterval(0),
	)
	if err != nil {
		t.Fatalf("tag.NewAcquisitionRuntime() error = %v", err)
	}
	if err := runtime.Start(t.Context()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() {
		runtime.Stop()
		deviceService.stopMonitor(fastDatasource.ID)
		deviceService.stopMonitor(slowDatasource.ID)
	})

	initialReads := map[uint16]int{}
	for len(initialReads) < 2 {
		started := awaitRoutedModbusRead(t, hub, time.Second)
		if started.request.UnitID != 7 || started.request.FunctionCode != modbus.FunctionReadHoldingRegisters || started.request.Quantity != 1 {
			t.Fatalf("initial request = %#v", started.request)
		}
		if initialReads[started.request.Address] != 0 {
			t.Fatalf("duplicate initial request for address %d", started.request.Address)
		}
		initialReads[started.request.Address]++
		switch started.request.Address {
		case 0:
			started.respond <- []byte{0, 1}
			awaitLatestTagValue(t, values, fastTag.ID, uint16(1))
		case 100:
			calculatedBefore := len(values.History(calculatedTag.ID, 10))
			started.respond <- []byte{0, 10}
			awaitLatestTagValue(t, values, slowTag.ID, uint16(10))
			time.Sleep(20 * time.Millisecond)
			if got := len(values.History(calculatedTag.ID, 10)); got != calculatedBefore {
				t.Fatalf("slow non-trigger sample recalculated Tag: history %d -> %d", calculatedBefore, got)
			}
		default:
			t.Fatalf("unexpected initial Modbus address %d", started.request.Address)
		}
	}

	calculatedBefore := len(values.History(calculatedTag.ID, 10))
	fastPoll := awaitRoutedModbusRead(t, hub, 2*time.Second)
	if fastPoll.request.Address != 0 {
		t.Fatalf("next request address = %d, want fast Datasource address 0", fastPoll.request.Address)
	}
	fastPoll.respond <- []byte{0, 2}
	fastValue := awaitLatestTagValue(t, values, fastTag.ID, uint16(2))
	calculatedValue := awaitLatestTagValue(t, values, calculatedTag.ID, uint32(20))
	if got := len(values.History(calculatedTag.ID, 10)); got != calculatedBefore+1 {
		t.Fatalf("calculated history length = %d, want %d", got, calculatedBefore+1)
	}
	if !calculatedValue.ObservedAt.Equal(fastValue.ObservedAt) {
		t.Errorf("calculated observed_at = %s, want trigger time %s", calculatedValue.ObservedAt, fastValue.ObservedAt)
	}
	if slowValue, exists := values.Latest(slowTag.ID); !exists || slowValue.Value != uint16(10) || !slowValue.ObservedAt.Before(calculatedValue.StoredAt) {
		t.Errorf("slow snapshot value = %#v, want retained uint16(10)", slowValue)
	}
	if got := len(values.History(slowTag.ID, 10)); got != 1 {
		t.Errorf("slow Datasource history length = %d, want one 10-second sample", got)
	}
	if got := hub.clientCreations.Load(); got != 2 {
		t.Fatalf("Modbus clients created = %d, want exactly two Datasource monitors and no calculation reads", got)
	}
	select {
	case unexpected := <-hub.started:
		t.Fatalf("calculation issued an external Modbus request: %#v", unexpected.request)
	case <-time.After(100 * time.Millisecond):
	}

	runtime.Stop()
	deviceService.stopMonitor(fastDatasource.ID)
	deviceService.stopMonitor(slowDatasource.ID)
}

type controlledModbusRead struct {
	request modbus.ReadRequest
	at      time.Time
}

type controlledModbusClient struct {
	connected atomic.Bool
	reads     atomic.Int32
	started   chan controlledModbusRead
	release   chan struct{}
}

type routedModbusRead struct {
	request modbus.ReadRequest
	respond chan []byte
}

type routedModbusHub struct {
	clientCreations atomic.Int32
	started         chan routedModbusRead
}

func newRoutedModbusHub() *routedModbusHub {
	return &routedModbusHub{started: make(chan routedModbusRead)}
}

type routedModbusClient struct {
	hub       *routedModbusHub
	connected atomic.Bool
}

func (client *routedModbusClient) Connect(context.Context) error {
	client.connected.Store(true)
	return nil
}

func (client *routedModbusClient) Disconnect() error {
	client.connected.Store(false)
	return nil
}

func (client *routedModbusClient) IsConnected() bool {
	return client.connected.Load()
}

func (client *routedModbusClient) Read(ctx context.Context, request modbus.ReadRequest) ([]byte, error) {
	started := routedModbusRead{request: request, respond: make(chan []byte)}
	select {
	case client.hub.started <- started:
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

func newControlledModbusClient() *controlledModbusClient {
	return &controlledModbusClient{
		started: make(chan controlledModbusRead),
		release: make(chan struct{}),
	}
}

func (client *controlledModbusClient) Connect(context.Context) error {
	client.connected.Store(true)
	return nil
}

func (client *controlledModbusClient) Disconnect() error {
	client.connected.Store(false)
	return nil
}

func (client *controlledModbusClient) IsConnected() bool {
	return client.connected.Load()
}

func (client *controlledModbusClient) Read(ctx context.Context, request modbus.ReadRequest) ([]byte, error) {
	poll := client.reads.Load() + 1
	select {
	case client.started <- controlledModbusRead{request: request, at: time.Now()}:
		client.reads.Add(1)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case <-client.release:
		return []byte{0, byte(poll), 0, byte(poll + 10)}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type acquisitionTagRepository struct {
	tags []tag.Tag
}

func newAcquisitionTagRepository(datasourceID uuid.UUID) *acquisitionTagRepository {
	return &acquisitionTagRepository{tags: []tag.Tag{
		{ID: uuid.New(), DatasourceID: &datasourceID, Name: "Register zero", Type: tag.TypeReading, DataType: tag.DataTypeUInt16, Enabled: true, Config: json.RawMessage(`{"decoder":{"type":"binary_numeric","config":{"byte_offset":0}}}`)},
		{ID: uuid.New(), DatasourceID: &datasourceID, Name: "Register one", Type: tag.TypeReading, DataType: tag.DataTypeUInt16, Enabled: true, Config: json.RawMessage(`{"decoder":{"type":"binary_numeric","config":{"byte_offset":2}}}`)},
		{ID: uuid.New(), DatasourceID: &datasourceID, Name: "Combined registers", Type: tag.TypeReading, DataType: tag.DataTypeUInt32, Enabled: true, Config: json.RawMessage(`{"decoder":{"type":"binary_numeric","config":{"byte_offset":0}}}`)},
	}}
}

func (*acquisitionTagRepository) Create(context.Context, *tag.Tag, []uuid.UUID) error {
	return errors.New("not implemented")
}

func (repository *acquisitionTagRepository) Find(_ context.Context, id uuid.UUID) (*tag.Tag, error) {
	for _, entity := range repository.tags {
		if entity.ID == id {
			copy := entity
			return &copy, nil
		}
	}
	return nil, tag.ErrTagNotFound
}

func (repository *acquisitionTagRepository) List(context.Context, tag.ListInput) (*tag.ListResult, error) {
	return &tag.ListResult{Data: append([]tag.Tag(nil), repository.tags...), Total: int64(len(repository.tags)), TotalPages: 1}, nil
}

func (*acquisitionTagRepository) Update(context.Context, *tag.Tag, []uuid.UUID) error {
	return errors.New("not implemented")
}

func (*acquisitionTagRepository) Delete(context.Context, uuid.UUID) error {
	return errors.New("not implemented")
}

func (*acquisitionTagRepository) ListDependencies(context.Context) ([]tag.Dependency, error) {
	return []tag.Dependency{}, nil
}

func (repository *acquisitionTagRepository) ListEnabledReadingTags(context.Context) ([]tag.Tag, error) {
	result := make([]tag.Tag, 0, len(repository.tags))
	for _, entity := range repository.tags {
		if entity.Enabled && entity.Type == tag.TypeReading {
			result = append(result, entity)
		}
	}
	return result, nil
}

func (repository *acquisitionTagRepository) ListEnabledTags(context.Context) ([]tag.Tag, error) {
	return append([]tag.Tag(nil), repository.tags...), nil
}

func awaitModbusRead(t *testing.T, client *controlledModbusClient) controlledModbusRead {
	t.Helper()
	select {
	case started := <-client.started:
		return started
	case <-time.After(time.Second):
		t.Fatal("Modbus poll did not start")
		return controlledModbusRead{}
	}
}

func awaitRoutedModbusRead(t *testing.T, hub *routedModbusHub, timeout time.Duration) routedModbusRead {
	t.Helper()
	select {
	case started := <-hub.started:
		return started
	case <-time.After(timeout):
		t.Fatal("Modbus poll did not start")
		return routedModbusRead{}
	}
}

func awaitLatestTagValue(t *testing.T, store *tag.MemoryValueStore, tagID uuid.UUID, want any) tag.TagValue {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if value, exists := store.Latest(tagID); exists && value.Quality == tag.ValueQualityGood && reflect.DeepEqual(value.Value, want) {
			return value
		}
		time.Sleep(time.Millisecond)
	}
	value, _ := store.Latest(tagID)
	t.Fatalf("latest Tag %s value = %#v, want %#v", tagID, value, want)
	return tag.TagValue{}
}

func awaitTagHistoryLength(t *testing.T, store *tag.MemoryValueStore, tags []tag.Tag, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		complete := true
		for _, entity := range tags {
			if len(store.History(entity.ID, 10)) != want {
				complete = false
				break
			}
		}
		if complete {
			return
		}
		time.Sleep(time.Millisecond)
	}
	lengths := make([]int, 0, len(tags))
	for _, entity := range tags {
		lengths = append(lengths, len(store.History(entity.ID, 10)))
	}
	t.Fatalf("Tag history lengths = %v, want %d", lengths, want)
}

func assertAcquiredTagHistory(t *testing.T, store *tag.MemoryValueStore, entity tag.Tag, want []any) {
	t.Helper()
	history := store.History(entity.ID, 10)
	if len(history) != len(want) {
		t.Fatalf("Tag %s history length = %d, want %d", entity.Name, len(history), len(want))
	}
	for index, value := range history {
		if value.Quality != tag.ValueQualityGood || !reflect.DeepEqual(value.Value, want[index]) {
			t.Errorf("Tag %s sample %d = %#v, want %#v", entity.Name, index+1, value, want[index])
		}
	}
}
