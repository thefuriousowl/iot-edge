package device

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/protocol"
)

const monitorGracePeriod = 2 * time.Second

type monitorRuntime struct {
	ctx            context.Context
	cancel         context.CancelFunc
	subscribers    map[uint64]chan DatasourceSample
	nextSubscriber uint64
	stopTimer      *time.Timer
	latest         *DatasourceSample
	mu             sync.Mutex
}

type executionGate struct {
	token chan struct{}
}

func newExecutionGate() *executionGate {
	gate := &executionGate{token: make(chan struct{}, 1)}
	gate.token <- struct{}{}
	return gate
}

func (g *executionGate) Execute(ctx context.Context, operation func(context.Context) error) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-g.token:
	}
	defer func() { g.token <- struct{}{} }()
	return operation(ctx)
}

func (s *Service) Subscribe(ctx context.Context, id uuid.UUID) (<-chan DatasourceSample, func(), error) {
	entity, err := s.repository.FindDatasource(ctx, id)
	if err != nil {
		return nil, nil, mapRepositoryError(err)
	}
	if !monitoringEnabled(entity) {
		return nil, nil, ErrMonitoringDisabled
	}
	driver, err := s.driverForDatasource(&entity.Device, entity.Type)
	if err != nil {
		return nil, nil, err
	}

	s.monitorMu.Lock()
	runtime := s.monitors[id]
	if runtime == nil {
		monitorCtx, cancel := context.WithCancel(context.Background())
		runtime = &monitorRuntime{ctx: monitorCtx, cancel: cancel, subscribers: map[uint64]chan DatasourceSample{}}
		s.monitors[id] = runtime
		go s.runMonitor(id, runtime, driver, entity)
	}
	runtime.mu.Lock()
	if runtime.stopTimer != nil {
		runtime.stopTimer.Stop()
		runtime.stopTimer = nil
	}
	runtime.nextSubscriber++
	subscriberID := runtime.nextSubscriber
	channel := make(chan DatasourceSample, 1)
	runtime.subscribers[subscriberID] = channel
	if runtime.latest != nil {
		channel <- *runtime.latest
	}
	runtime.mu.Unlock()
	s.monitorMu.Unlock()

	var once sync.Once
	unsubscribe := func() { once.Do(func() { s.unsubscribe(id, runtime, subscriberID) }) }
	return channel, unsubscribe, nil
}

func (s *Service) runMonitor(id uuid.UUID, runtime *monitorRuntime, driver protocol.DatasourceDriver, entity *DatasourceContext) {
	interval := datasourceInterval(entity.Config, entity.Device.Config)
	err := driver.Monitor(runtime.ctx, s.datasourceReadRequest(&entity.Device, entity.Config), interval, func(sample protocol.DatasourceSample) {
		s.recordGatewayRequest(entity.Device.Gateway.ID, sample, 0, nil)
		formatted := s.formatSample(id, sample)
		s.storeSample(formatted)
		runtime.mu.Lock()
		runtime.latest = &formatted
		for _, subscriber := range runtime.subscribers {
			select {
			case subscriber <- formatted:
			default:
				select {
				case <-subscriber:
				default:
				}
				select {
				case subscriber <- formatted:
				default:
				}
			}
		}
		runtime.mu.Unlock()
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		formatted := s.formatSample(id, protocol.DatasourceSample{ObservedAt: time.Now().UTC(), Quality: "bad", Error: "Monitoring stopped"})
		s.storeSample(formatted)
		runtime.mu.Lock()
		for _, subscriber := range runtime.subscribers {
			select {
			case subscriber <- formatted:
			default:
			}
		}
		runtime.mu.Unlock()
	}
	s.monitorMu.Lock()
	if s.monitors[id] == runtime {
		delete(s.monitors, id)
	}
	s.monitorMu.Unlock()
	runtime.mu.Lock()
	for key, subscriber := range runtime.subscribers {
		close(subscriber)
		delete(runtime.subscribers, key)
	}
	runtime.mu.Unlock()
}

func (s *Service) unsubscribe(id uuid.UUID, runtime *monitorRuntime, subscriberID uint64) {
	runtime.mu.Lock()
	if subscriber, ok := runtime.subscribers[subscriberID]; ok {
		delete(runtime.subscribers, subscriberID)
		close(subscriber)
	}
	empty := len(runtime.subscribers) == 0
	if empty && runtime.stopTimer == nil {
		runtime.stopTimer = time.AfterFunc(monitorGracePeriod, func() {
			runtime.mu.Lock()
			stillEmpty := len(runtime.subscribers) == 0
			runtime.mu.Unlock()
			if stillEmpty {
				s.stopMonitorIfCurrent(id, runtime)
			}
		})
	}
	runtime.mu.Unlock()
}

func (s *Service) stopMonitor(id uuid.UUID) {
	s.monitorMu.Lock()
	runtime := s.monitors[id]
	if runtime != nil {
		delete(s.monitors, id)
	}
	s.monitorMu.Unlock()
	if runtime != nil {
		runtime.cancel()
	}
}

func (s *Service) stopMonitorIfCurrent(id uuid.UUID, runtime *monitorRuntime) {
	s.monitorMu.Lock()
	if s.monitors[id] == runtime {
		delete(s.monitors, id)
		runtime.cancel()
	}
	s.monitorMu.Unlock()
}

func (s *Service) monitorStatus(id uuid.UUID, enabled bool) string {
	if !enabled {
		return "paused"
	}
	s.monitorMu.Lock()
	runtime := s.monitors[id]
	s.monitorMu.Unlock()
	if runtime == nil {
		return "idle"
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.latest != nil && runtime.latest.Quality == "bad" {
		return "error"
	}
	return "monitoring"
}

func datasourceInterval(datasourceConfig, deviceConfig json.RawMessage) time.Duration {
	var datasource struct {
		PollIntervalMS *int `json:"poll_interval_ms"`
	}
	var device struct {
		PollIntervalMS int `json:"poll_interval_ms"`
	}
	_ = json.Unmarshal(datasourceConfig, &datasource)
	_ = json.Unmarshal(deviceConfig, &device)
	milliseconds := device.PollIntervalMS
	if datasource.PollIntervalMS != nil {
		milliseconds = *datasource.PollIntervalMS
	}
	if milliseconds < 100 {
		milliseconds = 1000
	}
	return time.Duration(milliseconds) * time.Millisecond
}
