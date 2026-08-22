package device

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/protocol"
)

const (
	defaultDevicesPerPage = 20
	maxDevicesPerPage     = 100
)

var (
	ErrRepositoryRequired          = errors.New("device repository is required")
	ErrDriversRequired             = errors.New("datasource drivers are required")
	ErrDeviceNotFound              = errors.New("device not found")
	ErrDatasourceNotFound          = errors.New("datasource not found")
	ErrGatewayNotFound             = errors.New("vGateway not found")
	ErrDeviceNameExists            = errors.New("device name already exists")
	ErrDatasourceNameExists        = errors.New("datasource name already exists")
	ErrInvalidDevice               = errors.New("invalid device")
	ErrInvalidDatasource           = errors.New("invalid datasource")
	ErrUnsupportedDeviceType       = errors.New("unsupported device type")
	ErrUnsupportedDatasourceType   = errors.New("unsupported datasource type")
	ErrProtocolMismatch            = errors.New("protocol type mismatch")
	ErrMonitoringDisabled          = errors.New("datasource monitoring is disabled")
	ErrDatasourceReadFailed        = errors.New("datasource read failed")
	ErrDatasourceSampleUnavailable = errors.New("datasource sample is not available")
)

type CreateDeviceInput struct {
	Name        string
	Type        DeviceType
	Description *string
	Enabled     *bool
	Config      Config
}

type UpdateDeviceInput struct {
	Name        *string
	Description OptionalDescription
	Enabled     *bool
	Config      *Config
}

type CreateDatasourceInput struct {
	Name        string
	Type        DatasourceType
	Description *string
	Enabled     *bool
	Config      Config
}

type UpdateDatasourceInput struct {
	Name        *string
	Description OptionalDescription
	Enabled     *bool
	Config      *Config
}

type PreviewDatasourceInput struct {
	Type   DatasourceType
	Config Config
}

type OptionalDescription struct {
	Set   bool
	Value *string
}

type GatewayRequestRecorder interface {
	RecordGatewayRequest(
		uuid.UUID,
		time.Time,
		time.Duration,
		int,
		error,
	)
}

type Service struct {
	repository        Repository
	requestRecorder   GatewayRequestRecorder
	deviceDrivers     map[DeviceType]protocol.DatasourceDriver
	datasourceDrivers map[DatasourceType]protocol.DatasourceDriver
	monitorMu         sync.Mutex
	monitors          map[uuid.UUID]*monitorRuntime
	gateMu            sync.Mutex
	gates             map[uuid.UUID]*executionGate
	sampleMu          sync.RWMutex
	samples           map[uuid.UUID]DatasourceSample
	sequence          atomic.Uint64
}

func NewService(repository Repository, drivers ...protocol.DatasourceDriver) (*Service, error) {
	return newService(repository, nil, drivers...)
}

func NewServiceWithGatewayRequestRecorder(
	repository Repository,
	recorder GatewayRequestRecorder,
	drivers ...protocol.DatasourceDriver,
) (*Service, error) {
	return newService(repository, recorder, drivers...)
}

func newService(
	repository Repository,
	recorder GatewayRequestRecorder,
	drivers ...protocol.DatasourceDriver,
) (*Service, error) {
	if repository == nil {
		return nil, ErrRepositoryRequired
	}
	if len(drivers) == 0 {
		return nil, ErrDriversRequired
	}
	service := &Service{repository: repository, requestRecorder: recorder, deviceDrivers: map[DeviceType]protocol.DatasourceDriver{}, datasourceDrivers: map[DatasourceType]protocol.DatasourceDriver{}, monitors: map[uuid.UUID]*monitorRuntime{}, gates: map[uuid.UUID]*executionGate{}, samples: map[uuid.UUID]DatasourceSample{}}
	for _, driver := range drivers {
		if driver == nil {
			return nil, ErrDriversRequired
		}
		service.deviceDrivers[DeviceType(driver.DeviceType())] = driver
		for _, datasourceType := range driver.DatasourceTypes() {
			service.datasourceDrivers[DatasourceType(datasourceType)] = driver
		}
	}
	return service, nil
}

func (s *Service) CreateDevice(ctx context.Context, gatewayID uuid.UUID, input CreateDeviceInput) (*Device, error) {
	name, err := validateName(input.Name, ErrInvalidDevice)
	if err != nil {
		return nil, err
	}
	driver, ok := s.deviceDrivers[input.Type]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedDeviceType, input.Type)
	}
	gateway, err := s.repository.FindGateway(ctx, gatewayID)
	if err != nil {
		return nil, mapRepositoryError(err)
	}
	if gateway.Type != driver.GatewayType() {
		return nil, ErrProtocolMismatch
	}
	config, err := driver.NormalizeDeviceConfig(input.Config)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidDevice, err)
	}
	entity := &Device{VGatewayID: gatewayID, Name: name, Type: input.Type, Description: normalizeDescription(input.Description), Enabled: boolDefault(input.Enabled), Config: config}
	if err := s.repository.CreateDevice(ctx, entity); err != nil {
		return nil, mapRepositoryError(err)
	}
	return entity, nil
}

func (s *Service) GetDevice(ctx context.Context, id uuid.UUID) (*DeviceView, error) {
	entity, err := s.repository.FindDevice(ctx, id)
	if err != nil {
		return nil, mapRepositoryError(err)
	}
	datasources, err := s.repository.ListDatasources(ctx, id)
	if err != nil {
		return nil, err
	}
	return &DeviceView{Device: entity.Device, DatasourceCount: int64(len(datasources))}, nil
}

func (s *Service) ListDevices(ctx context.Context, gatewayID uuid.UUID) ([]DeviceView, error) {
	if _, err := s.repository.FindGateway(ctx, gatewayID); err != nil {
		return nil, mapRepositoryError(err)
	}
	return s.repository.ListDevices(ctx, gatewayID)
}

func (s *Service) ListDeviceInventory(ctx context.Context, input DeviceInventoryInput) (*DeviceInventoryResult, error) {
	if input.Type != nil {
		if _, ok := s.deviceDrivers[*input.Type]; !ok {
			return nil, fmt.Errorf("%w: %q", ErrUnsupportedDeviceType, *input.Type)
		}
	}
	if input.Page < 0 || input.PerPage < 0 || input.PerPage > maxDevicesPerPage {
		return nil, fmt.Errorf("%w: invalid pagination", ErrInvalidDevice)
	}
	if input.Page == 0 {
		input.Page = 1
	}
	if input.PerPage == 0 {
		input.PerPage = defaultDevicesPerPage
	}
	return s.repository.ListDeviceInventory(ctx, input)
}

func (s *Service) UpdateDevice(ctx context.Context, id uuid.UUID, input UpdateDeviceInput) (*Device, error) {
	entity, err := s.repository.FindDevice(ctx, id)
	if err != nil {
		return nil, mapRepositoryError(err)
	}
	if input.Name != nil {
		entity.Name, err = validateName(*input.Name, ErrInvalidDevice)
		if err != nil {
			return nil, err
		}
	}
	if input.Description.Set {
		entity.Description = normalizeDescription(input.Description.Value)
	}
	if input.Enabled != nil {
		entity.Enabled = *input.Enabled
	}
	if input.Config != nil {
		driver, ok := s.deviceDrivers[entity.Type]
		if !ok {
			return nil, fmt.Errorf("%w: %q", ErrUnsupportedDeviceType, entity.Type)
		}
		entity.Config, err = driver.NormalizeDeviceConfig(*input.Config)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidDevice, err)
		}
	}
	datasourceIDs, err := s.deviceDatasourceIDs(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.repository.UpdateDevice(ctx, &entity.Device); err != nil {
		return nil, mapRepositoryError(err)
	}
	s.stopMonitors(datasourceIDs)
	s.deleteSamples(datasourceIDs)
	return &entity.Device, nil
}

func (s *Service) DeleteDevice(ctx context.Context, id uuid.UUID) error {
	datasourceIDs, err := s.deviceDatasourceIDs(ctx, id)
	if err != nil {
		return err
	}
	s.stopMonitors(datasourceIDs)
	if err := s.repository.DeleteDevice(ctx, id); err != nil {
		return mapRepositoryError(err)
	}
	s.deleteSamples(datasourceIDs)
	return nil
}

func (s *Service) CreateDatasource(ctx context.Context, deviceID uuid.UUID, input CreateDatasourceInput) (*Datasource, error) {
	name, err := validateName(input.Name, ErrInvalidDatasource)
	if err != nil {
		return nil, err
	}
	parent, err := s.repository.FindDevice(ctx, deviceID)
	if err != nil {
		return nil, mapRepositoryError(err)
	}
	driver, err := s.driverForDatasource(parent, input.Type)
	if err != nil {
		return nil, err
	}
	config, err := driver.NormalizeDatasourceConfig(string(input.Type), input.Config)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidDatasource, err)
	}
	entity := &Datasource{DeviceID: deviceID, Name: name, Type: input.Type, Description: normalizeDescription(input.Description), Enabled: boolDefault(input.Enabled), Config: config}
	if err := s.repository.CreateDatasource(ctx, entity); err != nil {
		return nil, mapRepositoryError(err)
	}
	return entity, nil
}

func (s *Service) GetDatasource(ctx context.Context, id uuid.UUID) (*DatasourceView, error) {
	entity, err := s.repository.FindDatasource(ctx, id)
	if err != nil {
		return nil, mapRepositoryError(err)
	}
	return &DatasourceView{Datasource: entity.Datasource, Status: s.monitorStatus(id, monitoringEnabled(entity))}, nil
}

func (s *Service) ListDatasources(ctx context.Context, deviceID uuid.UUID) ([]DatasourceView, error) {
	parent, err := s.repository.FindDevice(ctx, deviceID)
	if err != nil {
		return nil, mapRepositoryError(err)
	}
	entities, err := s.repository.ListDatasources(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	views := make([]DatasourceView, 0, len(entities))
	for _, entity := range entities {
		enabled := entity.Enabled && parent.Enabled && parent.Gateway.Enabled
		views = append(views, DatasourceView{Datasource: entity, Status: s.monitorStatus(entity.ID, enabled)})
	}
	return views, nil
}

func (s *Service) UpdateDatasource(ctx context.Context, id uuid.UUID, input UpdateDatasourceInput) (*DatasourceView, error) {
	entity, err := s.repository.FindDatasource(ctx, id)
	if err != nil {
		return nil, mapRepositoryError(err)
	}
	if input.Name != nil {
		entity.Name, err = validateName(*input.Name, ErrInvalidDatasource)
		if err != nil {
			return nil, err
		}
	}
	if input.Description.Set {
		entity.Description = normalizeDescription(input.Description.Value)
	}
	if input.Enabled != nil {
		entity.Enabled = *input.Enabled
	}
	if input.Config != nil {
		driver, driverErr := s.driverForDatasource(&entity.Device, entity.Type)
		if driverErr != nil {
			return nil, driverErr
		}
		entity.Config, err = driver.NormalizeDatasourceConfig(string(entity.Type), *input.Config)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidDatasource, err)
		}
	}
	s.stopMonitor(id)
	if err := s.repository.UpdateDatasource(ctx, &entity.Datasource); err != nil {
		return nil, mapRepositoryError(err)
	}
	s.deleteSample(id)
	return &DatasourceView{Datasource: entity.Datasource, Status: s.monitorStatus(id, monitoringEnabled(entity))}, nil
}

func (s *Service) DeleteDatasource(ctx context.Context, id uuid.UUID) error {
	s.stopMonitor(id)
	if err := s.repository.DeleteDatasource(ctx, id); err != nil {
		return mapRepositoryError(err)
	}
	s.deleteSample(id)
	return nil
}

func (s *Service) PreviewDatasource(ctx context.Context, deviceID uuid.UUID, input PreviewDatasourceInput) (*DatasourceSample, error) {
	parent, err := s.repository.FindDevice(ctx, deviceID)
	if err != nil {
		return nil, mapRepositoryError(err)
	}
	driver, err := s.driverForDatasource(parent, input.Type)
	if err != nil {
		return nil, err
	}
	config, err := driver.NormalizeDatasourceConfig(string(input.Type), input.Config)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidDatasource, err)
	}
	return s.readOnce(ctx, driver, parent, config, uuid.Nil)
}

func (s *Service) PreviewSavedDatasource(ctx context.Context, id uuid.UUID) (*DatasourceSample, error) {
	sample, err := s.ReadSavedDatasource(ctx, id)
	if err != nil {
		return nil, err
	}
	formatted := s.formatSample(id, sample)
	s.storeSample(formatted)
	return &formatted, nil
}

func (s *Service) ReadSavedDatasource(ctx context.Context, id uuid.UUID) (protocol.DatasourceSample, error) {
	return s.readSavedDatasource(ctx, id, false)
}

func (s *Service) ReadDatasourceForTag(ctx context.Context, id uuid.UUID) (protocol.DatasourceSample, error) {
	return s.readSavedDatasource(ctx, id, true)
}

func (s *Service) readSavedDatasource(ctx context.Context, id uuid.UUID, requireEnabled bool) (protocol.DatasourceSample, error) {
	entity, err := s.repository.FindDatasource(ctx, id)
	if err != nil {
		return protocol.DatasourceSample{}, mapRepositoryError(err)
	}
	if requireEnabled && !monitoringEnabled(entity) {
		return protocol.DatasourceSample{}, ErrMonitoringDisabled
	}
	driver, err := s.driverForDatasource(&entity.Device, entity.Type)
	if err != nil {
		return protocol.DatasourceSample{}, err
	}
	startedAt := time.Now()
	sample, err := driver.Preview(ctx, s.datasourceReadRequest(&entity.Device, entity.Config))
	s.recordGatewayRequest(entity.Device.Gateway.ID, sample, time.Since(startedAt), err)
	if err != nil {
		return protocol.DatasourceSample{}, fmt.Errorf("%w: %w", ErrDatasourceReadFailed, err)
	}
	return sample, nil
}

func (s *Service) readOnce(ctx context.Context, driver protocol.DatasourceDriver, parent *DeviceContext, config Config, id uuid.UUID) (*DatasourceSample, error) {
	startedAt := time.Now()
	sample, err := driver.Preview(ctx, s.datasourceReadRequest(parent, config))
	s.recordGatewayRequest(parent.Gateway.ID, sample, time.Since(startedAt), err)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrDatasourceReadFailed, err)
	}
	formatted := s.formatSample(id, sample)
	if id != uuid.Nil {
		s.storeSample(formatted)
	}
	return &formatted, nil
}

func (s *Service) recordGatewayRequest(
	gatewayID uuid.UUID,
	sample protocol.DatasourceSample,
	elapsed time.Duration,
	requestErr error,
) {
	if s.requestRecorder == nil {
		return
	}
	observedAt := sample.ObservedAt
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}
	latency := sample.Latency
	if latency <= 0 && elapsed > 0 {
		latency = elapsed
	}
	if requestErr == nil && sample.Quality == "bad" {
		requestErr = ErrDatasourceReadFailed
	}
	s.requestRecorder.RecordGatewayRequest(
		gatewayID,
		observedAt,
		latency,
		len(sample.Raw),
		requestErr,
	)
}

func (s *Service) LatestSample(ctx context.Context, id uuid.UUID) (*DatasourceSample, error) {
	if _, err := s.repository.FindDatasource(ctx, id); err != nil {
		return nil, mapRepositoryError(err)
	}
	s.sampleMu.RLock()
	sample, ok := s.samples[id]
	s.sampleMu.RUnlock()
	if !ok {
		return nil, ErrDatasourceSampleUnavailable
	}
	return &sample, nil
}

func (s *Service) datasourceReadRequest(parent *DeviceContext, config Config) protocol.DatasourceReadRequest {
	return protocol.DatasourceReadRequest{
		GatewayConfig:    parent.Gateway.Config,
		DeviceConfig:     parent.Config,
		DatasourceConfig: config,
		ExecuteExclusive: s.gatewayGate(parent.Gateway.ID).Execute,
	}
}

func (s *Service) gatewayGate(id uuid.UUID) *executionGate {
	s.gateMu.Lock()
	defer s.gateMu.Unlock()
	gate := s.gates[id]
	if gate == nil {
		gate = newExecutionGate()
		s.gates[id] = gate
	}
	return gate
}

func (s *Service) storeSample(sample DatasourceSample) {
	s.sampleMu.Lock()
	defer s.sampleMu.Unlock()
	if sample.Quality == "bad" {
		if previous, ok := s.samples[sample.DatasourceID]; ok {
			sample.RawHex = previous.RawHex
			sample.Data = previous.Data
		}
	}
	s.samples[sample.DatasourceID] = sample
}

func (s *Service) deleteSample(id uuid.UUID) {
	s.sampleMu.Lock()
	delete(s.samples, id)
	s.sampleMu.Unlock()
}

func (s *Service) driverForDatasource(parent *DeviceContext, datasourceType DatasourceType) (protocol.DatasourceDriver, error) {
	driver, ok := s.datasourceDrivers[datasourceType]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedDatasourceType, datasourceType)
	}
	if parent.Type != DeviceType(driver.DeviceType()) || parent.Gateway.Type != driver.GatewayType() {
		return nil, ErrProtocolMismatch
	}
	return driver, nil
}

func monitoringEnabled(entity *DatasourceContext) bool {
	return entity.Enabled && entity.Device.Enabled && entity.Device.Gateway.Enabled
}

func (s *Service) formatSample(id uuid.UUID, sample protocol.DatasourceSample) DatasourceSample {
	return DatasourceSample{DatasourceID: id, Sequence: s.sequence.Add(1), ObservedAt: sample.ObservedAt, LatencyMS: float64(sample.Latency) / float64(time.Millisecond), Quality: sample.Quality, RawHex: strings.ToUpper(hex.EncodeToString(sample.Raw)), Data: sample.Data, Error: sample.Error}
}

func validateName(raw string, sentinel error) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", fmt.Errorf("%w: name is required", sentinel)
	}
	if utf8.RuneCountInString(name) > 100 {
		return "", fmt.Errorf("%w: name must not exceed 100 characters", sentinel)
	}
	return name, nil
}

func normalizeDescription(value *string) *string {
	if value == nil {
		return nil
	}
	normalized := strings.TrimSpace(*value)
	if normalized == "" {
		return nil
	}
	return &normalized
}
func boolDefault(value *bool) bool       { return value == nil || *value }
func mapRepositoryError(err error) error { return err }

func (s *Service) deviceDatasourceIDs(ctx context.Context, deviceID uuid.UUID) ([]uuid.UUID, error) {
	datasources, err := s.repository.ListDatasources(ctx, deviceID)
	if err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, 0, len(datasources))
	for _, datasource := range datasources {
		ids = append(ids, datasource.ID)
	}
	return ids, nil
}

func (s *Service) stopMonitors(ids []uuid.UUID) {
	for _, id := range ids {
		s.stopMonitor(id)
	}
}

func (s *Service) deleteSamples(ids []uuid.UUID) {
	for _, id := range ids {
		s.deleteSample(id)
	}
}
