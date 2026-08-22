package tag

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/protocol"
)

const defaultAcquisitionReconcileInterval = 5 * time.Second
const maxCalculatedCascadeValues = 1024

var (
	ErrAcquisitionRepositoryRequired = errors.New("acquisition repository is required")
	ErrAcquisitionProcessorRequired  = errors.New("acquisition tag processor is required")
	ErrAcquisitionSourceRequired     = errors.New("acquisition datasource subscriber is required")
	ErrAcquisitionValueStoreRequired = errors.New("acquisition value store is required")
	ErrAcquisitionAlreadyStarted     = errors.New("acquisition runtime is already started")
	ErrAcquisitionNotStarted         = errors.New("acquisition runtime is not started")
	ErrAcquisitionStreamClosed       = errors.New("acquisition datasource stream closed")
)

type RuntimeValueStore interface {
	Put(TagValue) (TagValue, error)
	Snapshot([]uuid.UUID) ValueSnapshot
}

type AcquisitionOption func(*AcquisitionRuntime) error

func WithAcquisitionReconcileInterval(interval time.Duration) AcquisitionOption {
	return func(runtime *AcquisitionRuntime) error {
		if interval < 0 {
			return fmt.Errorf("%w: reconcile interval must not be negative", ErrInvalidTagInput)
		}
		runtime.reconcileInterval = interval
		return nil
	}
}

type AcquisitionRuntime struct {
	repository        Repository
	processor         *Service
	source            DatasourceSubscriber
	values            RuntimeValueStore
	reconcileInterval time.Duration
	errors            chan error
	events            chan TagValue

	mu            sync.Mutex
	reconcileMu   sync.Mutex
	ctx           context.Context
	cancel        context.CancelFunc
	started       bool
	stopped       bool
	subscriptions map[uuid.UUID]*readingSubscription
	calculated    map[uuid.UUID][]runtimeCalculatedTag
	constants     map[uuid.UUID]string
	invalid       map[uuid.UUID]string
	waitGroup     sync.WaitGroup
}

type runtimeCalculatedTag struct {
	entity       Tag
	dependencies []uuid.UUID
}

type runtimeConstantTag struct {
	entity      Tag
	value       any
	fingerprint string
}

type calculatedRuntimeDefinition struct {
	triggerID uuid.UUID
	tag       runtimeCalculatedTag
}

type readingSubscription struct {
	ctx         context.Context
	cancel      context.CancelFunc
	unsubscribe func()
	stopOnce    sync.Once
	tagsMu      sync.RWMutex
	tags        []Tag
}

func NewAcquisitionRuntime(repository Repository, processor *Service, source DatasourceSubscriber, values RuntimeValueStore, options ...AcquisitionOption) (*AcquisitionRuntime, error) {
	if repository == nil {
		return nil, ErrAcquisitionRepositoryRequired
	}
	if processor == nil {
		return nil, ErrAcquisitionProcessorRequired
	}
	if source == nil {
		return nil, ErrAcquisitionSourceRequired
	}
	if values == nil {
		return nil, ErrAcquisitionValueStoreRequired
	}
	runtime := &AcquisitionRuntime{repository: repository, processor: processor, source: source, values: values, reconcileInterval: defaultAcquisitionReconcileInterval, errors: make(chan error, 32), events: make(chan TagValue, 256), subscriptions: make(map[uuid.UUID]*readingSubscription), calculated: make(map[uuid.UUID][]runtimeCalculatedTag), constants: make(map[uuid.UUID]string), invalid: make(map[uuid.UUID]string)}
	for _, option := range options {
		if option == nil {
			continue
		}
		if err := option(runtime); err != nil {
			return nil, err
		}
	}
	return runtime, nil
}

func (runtime *AcquisitionRuntime) Start(ctx context.Context) error {
	runtime.mu.Lock()
	if runtime.started || runtime.stopped {
		runtime.mu.Unlock()
		return ErrAcquisitionAlreadyStarted
	}
	runtime.ctx, runtime.cancel = context.WithCancel(ctx)
	runtime.started = true
	runtime.waitGroup.Add(1)
	go runtime.runValueEvents()
	runtime.mu.Unlock()

	if err := runtime.Reconcile(ctx); err != nil {
		runtime.Stop()
		return err
	}
	runtime.mu.Lock()
	if runtime.reconcileInterval > 0 && !runtime.stopped {
		runtime.waitGroup.Add(1)
		go runtime.runReconciler()
	}
	runtime.mu.Unlock()
	return nil
}

func (runtime *AcquisitionRuntime) Stop() {
	runtime.mu.Lock()
	if !runtime.started || runtime.stopped {
		runtime.mu.Unlock()
		return
	}
	runtime.stopped = true
	runtime.cancel()
	subscriptions := make([]*readingSubscription, 0, len(runtime.subscriptions))
	for datasourceID, subscription := range runtime.subscriptions {
		delete(runtime.subscriptions, datasourceID)
		subscriptions = append(subscriptions, subscription)
	}
	runtime.mu.Unlock()
	for _, subscription := range subscriptions {
		subscription.stop()
	}
	runtime.waitGroup.Wait()
}

func (runtime *AcquisitionRuntime) Errors() <-chan error {
	return runtime.errors
}

func (runtime *AcquisitionRuntime) Reconcile(ctx context.Context) error {
	runtime.reconcileMu.Lock()
	defer runtime.reconcileMu.Unlock()

	runtime.mu.Lock()
	if !runtime.started || runtime.stopped {
		runtime.mu.Unlock()
		return ErrAcquisitionNotStarted
	}
	runtimeContext := runtime.ctx
	runtime.mu.Unlock()

	tags, err := runtime.repository.ListEnabledTags(ctx)
	if err != nil {
		return err
	}
	grouped := make(map[uuid.UUID][]Tag)
	calculated := make(map[uuid.UUID][]runtimeCalculatedTag)
	constants := make([]runtimeConstantTag, 0)
	invalid := make(map[uuid.UUID]string)
	invalidErrors := make(map[uuid.UUID]error)
	for _, entity := range tags {
		switch entity.Type {
		case TypeReading:
			if entity.DatasourceID == nil || *entity.DatasourceID == uuid.Nil {
				runtime.report(fmt.Errorf("%w: reading tag %s has no datasource", ErrInvalidTagInput, entity.ID))
				continue
			}
			grouped[*entity.DatasourceID] = append(grouped[*entity.DatasourceID], entity)
		case TypeConstant:
			_, value, normalizeErr := normalizeConstantConfig(entity.DataType, entity.Config)
			if normalizeErr != nil {
				runtime.report(fmt.Errorf("normalizing constant tag %s: %w", entity.ID, normalizeErr))
				continue
			}
			constants = append(constants, runtimeConstantTag{entity: entity, value: value, fingerprint: string(entity.DataType) + ":" + string(entity.Config)})
		case TypeCalculated:
			definition, definitionErr := runtimeCalculatedDefinition(entity)
			if definitionErr != nil {
				invalid[entity.ID] = string(entity.DataType) + ":" + string(entity.Config) + ":" + definitionErr.Error()
				invalidErrors[entity.ID] = fmt.Errorf("loading calculated tag %s: %w", entity.ID, definitionErr)
				continue
			}
			calculated[definition.triggerID] = append(calculated[definition.triggerID], definition.tag)
		}
	}

	runtime.mu.Lock()
	runtime.calculated = calculated
	newInvalidErrors := make([]error, 0, len(invalidErrors))
	for tagID, invalidErr := range invalidErrors {
		if runtime.invalid[tagID] != invalid[tagID] {
			newInvalidErrors = append(newInvalidErrors, invalidErr)
		}
	}
	runtime.invalid = invalid
	nextConstants := make(map[uuid.UUID]string, len(constants))
	constantsToPublish := make([]runtimeConstantTag, 0)
	for _, constant := range constants {
		nextConstants[constant.entity.ID] = constant.fingerprint
		if runtime.constants[constant.entity.ID] != constant.fingerprint {
			constantsToPublish = append(constantsToPublish, constant)
		}
	}
	runtime.constants = nextConstants
	removed := make([]*readingSubscription, 0)
	for datasourceID, subscription := range runtime.subscriptions {
		if datasourceTags, exists := grouped[datasourceID]; exists {
			subscription.setTags(datasourceTags)
			delete(grouped, datasourceID)
			continue
		}
		delete(runtime.subscriptions, datasourceID)
		removed = append(removed, subscription)
	}
	runtime.mu.Unlock()
	for _, invalidErr := range newInvalidErrors {
		runtime.report(invalidErr)
	}
	for _, constant := range constantsToPublish {
		runtime.storeAndQueue(TagValue{TagID: constant.entity.ID, ObservedAt: time.Now().UTC(), Quality: ValueQualityGood, DataType: constant.entity.DataType, Value: constant.value}, "constant")
	}
	for _, subscription := range removed {
		subscription.stop()
	}

	for datasourceID, datasourceTags := range grouped {
		subscriptionContext, cancel := context.WithCancel(runtimeContext)
		stream, unsubscribe, subscribeErr := runtime.source.SubscribeDatasourceForTags(subscriptionContext, datasourceID)
		if subscribeErr != nil {
			cancel()
			runtime.publishUnavailable(datasourceTags, subscribeErr)
			runtime.report(fmt.Errorf("subscribing datasource %s: %w", datasourceID, subscribeErr))
			continue
		}
		subscription := &readingSubscription{ctx: subscriptionContext, cancel: cancel, unsubscribe: unsubscribe}
		subscription.setTags(datasourceTags)
		runtime.mu.Lock()
		if runtime.stopped {
			runtime.mu.Unlock()
			subscription.stop()
			continue
		}
		if existing := runtime.subscriptions[datasourceID]; existing != nil {
			runtime.mu.Unlock()
			subscription.stop()
			existing.setTags(datasourceTags)
			continue
		}
		runtime.subscriptions[datasourceID] = subscription
		runtime.waitGroup.Add(1)
		runtime.mu.Unlock()
		go runtime.consume(datasourceID, subscription, stream)
	}
	return nil
}

func (runtime *AcquisitionRuntime) runReconciler() {
	defer runtime.waitGroup.Done()
	ticker := time.NewTicker(runtime.reconcileInterval)
	defer ticker.Stop()
	for {
		select {
		case <-runtime.ctx.Done():
			return
		case <-ticker.C:
			if err := runtime.Reconcile(runtime.ctx); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, ErrAcquisitionNotStarted) {
				runtime.report(fmt.Errorf("reconciling acquisition runtime: %w", err))
			}
		}
	}
}

func (runtime *AcquisitionRuntime) runValueEvents() {
	defer runtime.waitGroup.Done()
	for {
		select {
		case <-runtime.ctx.Done():
			return
		case first := <-runtime.events:
			queue := []TagValue{first}
			processed := 0
			for len(queue) > 0 {
				trigger := queue[0]
				queue = queue[1:]
				for _, calculated := range orderCalculatedTags(runtime.calculatedForTrigger(trigger.TagID)) {
					entry := runtime.evaluateCalculated(trigger, calculated)
					if stored, ok := runtime.storeValue(entry, "calculated"); ok {
						queue = append(queue, stored)
						processed++
						if processed >= maxCalculatedCascadeValues {
							runtime.report(fmt.Errorf("calculated cascade exceeded %d values", maxCalculatedCascadeValues))
							queue = nil
							break
						}
					}
				}
			}
		}
	}
}

func (runtime *AcquisitionRuntime) consume(datasourceID uuid.UUID, subscription *readingSubscription, stream <-chan protocol.DatasourceSample) {
	defer runtime.waitGroup.Done()
	defer runtime.removeSubscription(datasourceID, subscription)
	for {
		select {
		case <-subscription.ctx.Done():
			return
		case sample, open := <-stream:
			if !open {
				if subscription.ctx.Err() == nil {
					err := fmt.Errorf("%w: %s", ErrAcquisitionStreamClosed, datasourceID)
					runtime.publishUnavailable(subscription.currentTags(), err)
					runtime.report(err)
				}
				return
			}
			runtime.processSample(subscription.currentTags(), sample)
		}
	}
}

func (runtime *AcquisitionRuntime) processSample(tags []Tag, sample protocol.DatasourceSample) {
	observedAt := sample.ObservedAt
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}
	for _, entity := range tags {
		value, err := runtime.processor.ProcessReadingSample(entity.DataType, entity.Config, sample)
		entry := TagValue{TagID: entity.ID, ObservedAt: observedAt, Quality: ValueQualityGood, DataType: entity.DataType, Value: value}
		if err != nil {
			entry.Quality = ValueQualityBad
			entry.Error = err.Error()
		}
		runtime.storeAndQueue(entry, "reading")
	}
}

func (runtime *AcquisitionRuntime) publishUnavailable(tags []Tag, cause error) {
	observedAt := time.Now().UTC()
	for _, entity := range tags {
		runtime.storeAndQueue(TagValue{TagID: entity.ID, ObservedAt: observedAt, Quality: ValueQualityBad, DataType: entity.DataType, Error: cause.Error()}, "unavailable reading")
	}
}

func (runtime *AcquisitionRuntime) storeAndQueue(entry TagValue, source string) {
	stored, ok := runtime.storeValue(entry, source)
	if !ok {
		return
	}
	select {
	case runtime.events <- stored:
	case <-runtime.ctx.Done():
	}
}

func (runtime *AcquisitionRuntime) storeValue(entry TagValue, source string) (TagValue, bool) {
	stored, err := runtime.values.Put(entry)
	if err != nil {
		runtime.report(fmt.Errorf("storing %s tag %s: %w", source, entry.TagID, err))
		return TagValue{}, false
	}
	return stored, true
}

func (runtime *AcquisitionRuntime) calculatedForTrigger(triggerID uuid.UUID) []runtimeCalculatedTag {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	return append([]runtimeCalculatedTag(nil), runtime.calculated[triggerID]...)
}

func (runtime *AcquisitionRuntime) evaluateCalculated(trigger TagValue, calculated runtimeCalculatedTag) TagValue {
	entry := TagValue{TagID: calculated.entity.ID, ObservedAt: trigger.ObservedAt, Quality: ValueQualityGood, DataType: calculated.entity.DataType}
	if trigger.Quality != ValueQualityGood {
		entry.Quality = ValueQualityBad
		entry.Error = fmt.Sprintf("trigger tag %s is unavailable: %s", trigger.TagID, trigger.Error)
		return entry
	}
	snapshot := runtime.values.Snapshot(calculated.dependencies)
	value, err := runtime.processor.EvaluateCalculated(runtime.ctx, calculated.entity.DataType, calculated.entity.Config, snapshot.Resolve)
	if err != nil {
		entry.Quality = ValueQualityBad
		entry.Error = err.Error()
		return entry
	}
	entry.Value = value
	return entry
}

func runtimeCalculatedDefinition(entity Tag) (calculatedRuntimeDefinition, error) {
	var config calculatedConfigInput
	if err := json.Unmarshal(entity.Config, &config); err != nil {
		return calculatedRuntimeDefinition{}, fmt.Errorf("%w: %v", ErrInvalidTagInput, err)
	}
	if config.Trigger == nil || config.Trigger.TagID == uuid.Nil {
		return calculatedRuntimeDefinition{}, ErrCalculatedTriggerInvalid
	}
	expression, err := ParseExpression(config.Expression)
	if err != nil {
		return calculatedRuntimeDefinition{}, err
	}
	return calculatedRuntimeDefinition{triggerID: config.Trigger.TagID, tag: runtimeCalculatedTag{entity: entity, dependencies: expression.Dependencies()}}, nil
}

func orderCalculatedTags(tags []runtimeCalculatedTag) []runtimeCalculatedTag {
	remaining := make(map[uuid.UUID]runtimeCalculatedTag, len(tags))
	for _, entity := range tags {
		remaining[entity.entity.ID] = entity
	}
	ordered := make([]runtimeCalculatedTag, 0, len(tags))
	for len(remaining) > 0 {
		progress := false
		for _, candidate := range tags {
			current, exists := remaining[candidate.entity.ID]
			if !exists {
				continue
			}
			blocked := false
			for _, dependencyID := range current.dependencies {
				if _, pending := remaining[dependencyID]; pending {
					blocked = true
					break
				}
			}
			if blocked {
				continue
			}
			ordered = append(ordered, current)
			delete(remaining, candidate.entity.ID)
			progress = true
		}
		if progress {
			continue
		}
		for _, candidate := range tags {
			if current, exists := remaining[candidate.entity.ID]; exists {
				ordered = append(ordered, current)
				delete(remaining, candidate.entity.ID)
			}
		}
	}
	return ordered
}

func (runtime *AcquisitionRuntime) removeSubscription(datasourceID uuid.UUID, subscription *readingSubscription) {
	runtime.mu.Lock()
	if runtime.subscriptions[datasourceID] == subscription {
		delete(runtime.subscriptions, datasourceID)
	}
	runtime.mu.Unlock()
	subscription.stop()
}

func (runtime *AcquisitionRuntime) report(err error) {
	select {
	case runtime.errors <- err:
	default:
	}
}

func (subscription *readingSubscription) setTags(tags []Tag) {
	subscription.tagsMu.Lock()
	subscription.tags = append(subscription.tags[:0], tags...)
	subscription.tagsMu.Unlock()
}

func (subscription *readingSubscription) currentTags() []Tag {
	subscription.tagsMu.RLock()
	defer subscription.tagsMu.RUnlock()
	return append([]Tag(nil), subscription.tags...)
}

func (subscription *readingSubscription) stop() {
	subscription.stopOnce.Do(func() {
		subscription.cancel()
		if subscription.unsubscribe != nil {
			subscription.unsubscribe()
		}
	})
}
