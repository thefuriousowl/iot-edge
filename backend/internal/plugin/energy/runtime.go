package energy

import (
	"context"
	"errors"
	"sync"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/datalogger"
	"github.com/thefuriousowl/iot-edge/internal/plugin"
)

var (
	ErrCommittedBatchFeedUnavailable = errors.New("Energy committed Data Logger batch feed is unavailable")
	ErrCommittedBatchFeedClosed      = errors.New("Energy committed Data Logger batch feed closed")
)

type defaultRuntimeFactory struct{}

type metricsPublisher interface {
	Publish(uuid.UUID, BatchMetrics)
}

type runtime struct {
	config     Config
	feed       datalogger.CommittedBatchFeed
	calculator *Calculator
	instanceID uuid.UUID
	publisher  metricsPublisher

	mu        sync.RWMutex
	latest    BatchMetrics
	hasLatest bool
}

func (defaultRuntimeFactory) NewRuntime(spec plugin.RuntimeSpec, host plugin.Host, config Config) (plugin.Runtime, error) {
	return newEnergyRuntime(spec, host, config, nil)
}

func (hub *LiveHub) NewRuntime(spec plugin.RuntimeSpec, host plugin.Host, config Config) (plugin.Runtime, error) {
	if hub == nil {
		return nil, ErrLiveHubRequired
	}
	runtime, err := newEnergyRuntime(spec, host, config, hub)
	if err != nil {
		return nil, err
	}
	hub.Reset(spec.InstanceID)
	return runtime, nil
}

func newEnergyRuntime(spec plugin.RuntimeSpec, host plugin.Host, config Config, publisher metricsPublisher) (plugin.Runtime, error) {
	if isNil(host) {
		return nil, plugin.ErrHostRequired
	}
	capability, exists := host.ResolveCapability(plugin.CapabilityLoggerCommittedBatches)
	if !exists {
		return nil, ErrCommittedBatchFeedUnavailable
	}
	feed, valid := capability.(datalogger.CommittedBatchFeed)
	if !valid || isNil(feed) {
		return nil, ErrCommittedBatchFeedUnavailable
	}
	calculator, err := NewCalculator(config)
	if err != nil {
		return nil, err
	}
	return &runtime{config: config, feed: feed, calculator: calculator, instanceID: spec.InstanceID, publisher: publisher}, nil
}

func (runtime *runtime) Run(ctx context.Context) error {
	if runtime == nil || ctx == nil {
		return plugin.ErrRuntimeUnavailable
	}
	subscription, err := runtime.feed.Subscribe(runtime.config.LoggerID)
	if err != nil {
		return err
	}
	defer subscription.Close()
	latest, err := runtime.feed.Latest(ctx, runtime.config.LoggerID)
	if err == nil {
		if err := runtime.observe(*latest); err != nil {
			return err
		}
	} else if !errors.Is(err, datalogger.ErrRawBatchNotFound) {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case batch, open := <-subscription.Events():
			if !open {
				if err := subscription.Err(); err != nil {
					return err
				}
				return ErrCommittedBatchFeedClosed
			}
			if err := runtime.observe(batch); err != nil {
				return err
			}
		}
	}
}

func (runtime *runtime) Latest() (BatchMetrics, bool) {
	if runtime == nil {
		return BatchMetrics{}, false
	}
	runtime.mu.RLock()
	defer runtime.mu.RUnlock()
	if !runtime.hasLatest {
		return BatchMetrics{}, false
	}
	return cloneBatchMetrics(runtime.latest), true
}

func (runtime *runtime) observe(batch datalogger.RawBatch) error {
	metrics, err := runtime.calculator.Evaluate(batch)
	if err != nil {
		return err
	}
	runtime.mu.Lock()
	published := false
	if !runtime.hasLatest || runtime.latest.BatchAt.Before(metrics.BatchAt) {
		runtime.latest = cloneBatchMetrics(metrics)
		runtime.hasLatest = true
		published = true
	}
	runtime.mu.Unlock()
	if published && runtime.publisher != nil {
		runtime.publisher.Publish(runtime.instanceID, metrics)
	}
	return nil
}

var _ RuntimeFactory = defaultRuntimeFactory{}
var _ RuntimeFactory = (*LiveHub)(nil)
var _ plugin.Runtime = (*runtime)(nil)
