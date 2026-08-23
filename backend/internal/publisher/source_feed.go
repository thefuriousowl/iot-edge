package publisher

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/plugin"
	"github.com/thefuriousowl/iot-edge/internal/tag"
)

const (
	defaultSourceEventBuffer = 64
	maxCatalogEntities       = 10000
	maxCatalogSearchLength   = 200
	catalogPageSize          = 100
)

var (
	ErrTagCatalogRequired    = errors.New("Tag source catalog is required")
	ErrTagValueFeedRequired  = errors.New("Tag value feed is required")
	ErrPluginCatalogRequired = errors.New("Plugin source catalog is required")
	ErrPluginFeedRequired    = errors.New("Plugin output feed is required")
	ErrInvalidCatalogInput   = errors.New("invalid Publisher source catalog input")
	ErrSourceCatalogTooLarge = errors.New("Publisher source catalog is too large")
)

type TagSourceCatalog interface {
	Get(context.Context, uuid.UUID) (*tag.Tag, error)
	List(context.Context, tag.ListInput) (*tag.ListResult, error)
}

type TagSourceValueFeed interface {
	Latest(uuid.UUID) (tag.TagValue, bool)
	SubscribeValues(context.Context, []uuid.UUID, uint64) tag.ValueSubscription
}

type PluginSourceCatalog interface {
	Get(context.Context, uuid.UUID) (*plugin.Instance, error)
	List(context.Context, plugin.ListInput) (*plugin.ListResult, error)
	OutputDescriptors(context.Context, uuid.UUID) ([]plugin.OutputDescriptor, error)
}

type SourceSubscription interface {
	Events() <-chan SourceSample
	Err() error
	Close()
}

type UnifiedSourceReader interface {
	Catalog(context.Context, SourceCatalogInput) ([]SourceCatalogEntry, error)
	Resolve(context.Context, []SourceSelection) ([]ResolvedSource, error)
	Latest(context.Context, SourceSelection) (SourceSample, error)
	Snapshot(context.Context, []SourceSelection) (SourceSnapshot, error)
	Subscribe(context.Context, []SourceSelection) (SourceSubscription, error)
}

type SourceFeedOption func(*SourceFeed) error

func WithSourceFeedClock(clock func() time.Time) SourceFeedOption {
	return func(feed *SourceFeed) error {
		if clock == nil {
			return ErrInvalidCatalogInput
		}
		feed.now = clock
		return nil
	}
}

func WithSourceEventBuffer(size int) SourceFeedOption {
	return func(feed *SourceFeed) error {
		if size < 1 {
			return ErrInvalidCatalogInput
		}
		feed.eventBuffer = size
		return nil
	}
}

type SourceFeed struct {
	tags         TagSourceCatalog
	tagValues    TagSourceValueFeed
	plugins      PluginSourceCatalog
	pluginValues plugin.OutputFeed
	now          func() time.Time
	eventBuffer  int
}

func NewSourceFeed(tags TagSourceCatalog, tagValues TagSourceValueFeed, plugins PluginSourceCatalog, pluginValues plugin.OutputFeed, options ...SourceFeedOption) (*SourceFeed, error) {
	if isNilSourceDependency(tags) {
		return nil, ErrTagCatalogRequired
	}
	if isNilSourceDependency(tagValues) {
		return nil, ErrTagValueFeedRequired
	}
	if isNilSourceDependency(plugins) {
		return nil, ErrPluginCatalogRequired
	}
	if isNilSourceDependency(pluginValues) {
		return nil, ErrPluginFeedRequired
	}
	feed := &SourceFeed{tags: tags, tagValues: tagValues, plugins: plugins, pluginValues: pluginValues, now: time.Now, eventBuffer: defaultSourceEventBuffer}
	for _, option := range options {
		if option == nil {
			continue
		}
		if err := option(feed); err != nil {
			return nil, err
		}
	}
	return feed, nil
}

func (feed *SourceFeed) Catalog(ctx context.Context, input SourceCatalogInput) ([]SourceCatalogEntry, error) {
	if feed == nil || ctx == nil {
		return nil, ErrInvalidCatalogInput
	}
	if input.Kind != nil && *input.Kind != SourceKindTag && *input.Kind != SourceKindPluginOutput {
		return nil, ErrInvalidCatalogInput
	}
	input.Search = strings.ToLower(strings.TrimSpace(input.Search))
	if len(input.Search) > maxCatalogSearchLength {
		return nil, ErrInvalidCatalogInput
	}
	entries := make([]SourceCatalogEntry, 0)
	if input.Kind == nil || *input.Kind == SourceKindTag {
		tags, err := feed.listTags(ctx)
		if err != nil {
			return nil, err
		}
		for _, entity := range tags {
			if input.Enabled != nil && entity.Enabled != *input.Enabled {
				continue
			}
			descriptor, err := descriptorFromTag(entity)
			if err != nil {
				return nil, err
			}
			if !matchesCatalogSearch(descriptor, input.Search) {
				continue
			}
			value, exists := latestTagValue(feed.tagValues, entity.ID)
			sample := feed.sampleForTag(descriptor, "", value, exists)
			entries = append(entries, SourceCatalogEntry{Descriptor: descriptor, Current: currentFromSample(sample)})
		}
	}
	if input.Kind == nil || *input.Kind == SourceKindPluginOutput {
		instances, err := feed.listPluginInstances(ctx)
		if err != nil {
			return nil, err
		}
		for _, instance := range instances {
			if input.Enabled != nil && instance.Enabled != *input.Enabled {
				continue
			}
			descriptors, err := feed.plugins.OutputDescriptors(ctx, instance.ID)
			if err != nil {
				return nil, err
			}
			batch, err := feed.latestPluginBatch(ctx, instance.ID)
			if err != nil {
				return nil, err
			}
			for _, output := range descriptors {
				descriptor, err := descriptorFromPluginOutput(instance, output)
				if err != nil {
					return nil, err
				}
				if !matchesCatalogSearch(descriptor, input.Search) {
					continue
				}
				sample := sampleForPluginOutput(descriptor, "", batch)
				entries = append(entries, SourceCatalogEntry{Descriptor: descriptor, Current: currentFromSample(sample)})
			}
		}
	}
	if len(entries) > maxCatalogEntities {
		return nil, ErrSourceCatalogTooLarge
	}
	sortCatalog(entries)
	return entries, nil
}

func (feed *SourceFeed) Resolve(ctx context.Context, selections []SourceSelection) ([]ResolvedSource, error) {
	if feed == nil || ctx == nil {
		return nil, ErrInvalidSourceSelection
	}
	normalized, err := NormalizeSourceSelections(selections)
	if err != nil {
		return nil, err
	}
	return feed.resolve(ctx, normalized)
}

func (feed *SourceFeed) Latest(ctx context.Context, selection SourceSelection) (SourceSample, error) {
	resolved, err := feed.Resolve(ctx, []SourceSelection{selection})
	if err != nil {
		return SourceSample{}, err
	}
	samples, err := feed.loadSamples(ctx, resolved)
	if err != nil {
		return SourceSample{}, err
	}
	return samples[0], nil
}

func (feed *SourceFeed) Snapshot(ctx context.Context, selections []SourceSelection) (SourceSnapshot, error) {
	resolved, err := feed.Resolve(ctx, selections)
	if err != nil {
		return SourceSnapshot{}, err
	}
	samples, err := feed.loadSamples(ctx, resolved)
	if err != nil {
		return SourceSnapshot{}, err
	}
	return SourceSnapshot{CapturedAt: feed.now().UTC(), Samples: samples}, nil
}

func (feed *SourceFeed) Subscribe(ctx context.Context, selections []SourceSelection) (SourceSubscription, error) {
	resolved, err := feed.Resolve(ctx, selections)
	if err != nil {
		return nil, err
	}
	subscriptionContext, cancel := context.WithCancel(ctx)
	bufferSize := feed.eventBuffer + len(resolved)
	subscription := &sourceSubscription{
		ctx: subscriptionContext, cancel: cancel, events: make(chan SourceSample, bufferSize),
		seen: make(map[SourceReference]sourceCursor, len(resolved)),
	}

	tagSelections := make(map[uuid.UUID]ResolvedSource)
	pluginSelections := make(map[uuid.UUID][]ResolvedSource)
	for _, source := range resolved {
		switch source.Descriptor.Reference.Kind {
		case SourceKindTag:
			tagSelections[source.Descriptor.Reference.TagID] = source
		case SourceKindPluginOutput:
			instanceID := source.Descriptor.Reference.PluginInstanceID
			pluginSelections[instanceID] = append(pluginSelections[instanceID], source)
		}
	}

	var tagUpstream tag.ValueSubscription
	if len(tagSelections) > 0 {
		tagIDs := make([]uuid.UUID, 0, len(tagSelections))
		for tagID := range tagSelections {
			tagIDs = append(tagIDs, tagID)
		}
		sort.Slice(tagIDs, func(first, second int) bool { return tagIDs[first].String() < tagIDs[second].String() })
		tagUpstream = feed.tagValues.SubscribeValues(subscriptionContext, tagIDs, 0)
		subscription.closers = append(subscription.closers, tagUpstream.Unsubscribe)
	}

	pluginUpstreams := make(map[uuid.UUID]plugin.OutputSubscription, len(pluginSelections))
	instanceIDs := make([]uuid.UUID, 0, len(pluginSelections))
	for instanceID := range pluginSelections {
		instanceIDs = append(instanceIDs, instanceID)
	}
	sort.Slice(instanceIDs, func(first, second int) bool { return instanceIDs[first].String() < instanceIDs[second].String() })
	for _, instanceID := range instanceIDs {
		upstream, subscribeErr := feed.pluginValues.Subscribe(instanceID)
		if subscribeErr != nil {
			subscription.closeUpstreams()
			return nil, subscribeErr
		}
		if isNilSourceDependency(upstream) {
			subscription.closeUpstreams()
			return nil, ErrSourceUpstreamClosed
		}
		pluginUpstreams[instanceID] = upstream
		subscription.closers = append(subscription.closers, upstream.Close)
	}

	initial, err := feed.initialSubscriptionSamples(subscriptionContext, resolved, tagUpstream.Replay, instanceIDs)
	if err != nil {
		subscription.closeUpstreams()
		return nil, err
	}
	for _, sample := range initial {
		subscription.publish(sample)
	}

	if len(tagSelections) > 0 {
		subscription.waitGroup.Add(1)
		go subscription.runTagStream(tagUpstream.Stream, tagSelections, feed)
	}
	for instanceID, upstream := range pluginUpstreams {
		subscription.waitGroup.Add(1)
		go subscription.runPluginStream(upstream, pluginSelections[instanceID])
	}
	go subscription.finish()
	return subscription, nil
}

func (feed *SourceFeed) resolve(ctx context.Context, selections []SourceSelection) ([]ResolvedSource, error) {
	resolved := make([]ResolvedSource, len(selections))
	pluginInstances := make(map[uuid.UUID]*plugin.Instance)
	pluginDescriptors := make(map[uuid.UUID]map[string]plugin.OutputDescriptor)
	for index, selection := range selections {
		var descriptor SourceDescriptor
		switch selection.Reference.Kind {
		case SourceKindTag:
			entity, err := feed.tags.Get(ctx, selection.Reference.TagID)
			if err != nil {
				return nil, sourceLookupError(selection.Reference, err)
			}
			descriptor, err = descriptorFromTag(*entity)
			if err != nil {
				return nil, err
			}
		case SourceKindPluginOutput:
			instanceID := selection.Reference.PluginInstanceID
			instance := pluginInstances[instanceID]
			if instance == nil {
				var err error
				instance, err = feed.plugins.Get(ctx, instanceID)
				if err != nil {
					return nil, sourceLookupError(selection.Reference, err)
				}
				pluginInstances[instanceID] = instance
				outputs, outputErr := feed.plugins.OutputDescriptors(ctx, instanceID)
				if outputErr != nil {
					return nil, outputErr
				}
				pluginDescriptors[instanceID] = make(map[string]plugin.OutputDescriptor, len(outputs))
				for _, output := range outputs {
					pluginDescriptors[instanceID][string(output.Key)] = output
				}
			}
			output, exists := pluginDescriptors[instanceID][selection.Reference.OutputKey]
			if !exists {
				return nil, fmt.Errorf("%w: %s", ErrSourceNotFound, selection.Reference)
			}
			var err error
			descriptor, err = descriptorFromPluginOutput(*instance, output)
			if err != nil {
				return nil, err
			}
		}
		resolved[index] = ResolvedSource{Alias: selection.Alias, Descriptor: descriptor}
	}
	return resolved, nil
}

func (feed *SourceFeed) loadSamples(ctx context.Context, sources []ResolvedSource) ([]SourceSample, error) {
	pluginBatches := make(map[uuid.UUID]*plugin.OutputBatch)
	for _, source := range sources {
		if source.Descriptor.Reference.Kind != SourceKindPluginOutput {
			continue
		}
		instanceID := source.Descriptor.Reference.PluginInstanceID
		if _, loaded := pluginBatches[instanceID]; loaded {
			continue
		}
		batch, err := feed.latestPluginBatch(ctx, instanceID)
		if err != nil {
			return nil, err
		}
		pluginBatches[instanceID] = batch
	}
	samples := make([]SourceSample, len(sources))
	for index, source := range sources {
		switch source.Descriptor.Reference.Kind {
		case SourceKindTag:
			value, exists := latestTagValue(feed.tagValues, source.Descriptor.Reference.TagID)
			samples[index] = feed.sampleForTag(source.Descriptor, source.Alias, value, exists)
		case SourceKindPluginOutput:
			samples[index] = sampleForPluginOutput(source.Descriptor, source.Alias, pluginBatches[source.Descriptor.Reference.PluginInstanceID])
		}
	}
	return samples, nil
}

func (feed *SourceFeed) initialSubscriptionSamples(ctx context.Context, sources []ResolvedSource, tagReplay []tag.TagValue, pluginInstanceIDs []uuid.UUID) ([]SourceSample, error) {
	tagValues := make(map[uuid.UUID]tag.TagValue, len(tagReplay))
	for _, value := range tagReplay {
		tagValues[value.TagID] = value
	}
	pluginBatches := make(map[uuid.UUID]*plugin.OutputBatch, len(pluginInstanceIDs))
	for _, instanceID := range pluginInstanceIDs {
		batch, err := feed.latestPluginBatch(ctx, instanceID)
		if err != nil {
			return nil, err
		}
		pluginBatches[instanceID] = batch
	}
	samples := make([]SourceSample, len(sources))
	for index, source := range sources {
		reference := source.Descriptor.Reference
		if reference.Kind == SourceKindTag {
			value, exists := tagValues[reference.TagID]
			samples[index] = feed.sampleForTag(source.Descriptor, source.Alias, value, exists)
			continue
		}
		samples[index] = sampleForPluginOutput(source.Descriptor, source.Alias, pluginBatches[reference.PluginInstanceID])
	}
	return samples, nil
}

func (feed *SourceFeed) listTags(ctx context.Context) ([]tag.Tag, error) {
	entities := make([]tag.Tag, 0)
	for page := 1; ; page++ {
		result, err := feed.tags.List(ctx, tag.ListInput{Page: page, PerPage: catalogPageSize})
		if err != nil {
			return nil, err
		}
		entities = append(entities, result.Data...)
		if len(entities) > maxCatalogEntities {
			return nil, ErrSourceCatalogTooLarge
		}
		if page >= result.TotalPages || len(result.Data) == 0 {
			return entities, nil
		}
	}
}

func (feed *SourceFeed) listPluginInstances(ctx context.Context) ([]plugin.Instance, error) {
	instances := make([]plugin.Instance, 0)
	for page := 1; ; page++ {
		result, err := feed.plugins.List(ctx, plugin.ListInput{Page: page, PerPage: catalogPageSize})
		if err != nil {
			return nil, err
		}
		instances = append(instances, result.Data...)
		if len(instances) > maxCatalogEntities {
			return nil, ErrSourceCatalogTooLarge
		}
		if page >= result.TotalPages || len(result.Data) == 0 {
			return instances, nil
		}
	}
}

func (feed *SourceFeed) latestPluginBatch(ctx context.Context, instanceID uuid.UUID) (*plugin.OutputBatch, error) {
	batch, err := feed.pluginValues.Latest(ctx, instanceID)
	if errors.Is(err, plugin.ErrOutputBatchNotFound) {
		return nil, nil
	}
	return batch, err
}

func (feed *SourceFeed) sampleForTag(descriptor SourceDescriptor, alias string, value tag.TagValue, exists bool) SourceSample {
	if !exists {
		return unavailableSample(descriptor, alias)
	}
	observedAt := value.ObservedAt.UTC()
	emittedAt := value.StoredAt.UTC()
	sample := SourceSample{
		Alias: alias, Reference: descriptor.Reference, Available: true, Sequence: value.Sequence,
		SchemaVersion: descriptor.SchemaVersion, DataType: descriptor.DataType, Value: value.Value,
		Quality: SourceQuality(value.Quality), Error: value.Error,
		ObservedAt: &observedAt, EmittedAt: &emittedAt, PeriodStart: cloneTime(&observedAt), PeriodEnd: cloneTime(&observedAt),
	}
	if SourceDataType(value.DataType) != descriptor.DataType {
		sample.Value = nil
		sample.Quality = SourceQualityBad
		sample.Error = "Tag value data type does not match the current Tag definition"
	}
	return sample
}

func sampleForPluginOutput(descriptor SourceDescriptor, alias string, batch *plugin.OutputBatch) SourceSample {
	if batch == nil {
		return unavailableSample(descriptor, alias)
	}
	var output *plugin.OutputValue
	for index := range batch.Values {
		if string(batch.Values[index].Key) == descriptor.Reference.OutputKey {
			output = &batch.Values[index]
			break
		}
	}
	if output == nil {
		return unavailableSample(descriptor, alias)
	}
	observedAt := output.ObservedAt.UTC()
	emittedAt := batch.PublishedAt.UTC()
	periodStart := output.PeriodStart.UTC()
	periodEnd := output.PeriodEnd.UTC()
	sample := SourceSample{
		Alias: alias, Reference: descriptor.Reference, Available: true, Sequence: batch.Sequence,
		SchemaVersion: output.SchemaVersion, DataType: SourceDataType(output.DataType), Unit: output.Unit,
		Value: output.Value, Quality: SourceQuality(output.Quality), Error: output.Error,
		ObservedAt: &observedAt, EmittedAt: &emittedAt, PeriodStart: &periodStart, PeriodEnd: &periodEnd,
		CoveragePercent: output.CoveragePercent, Attributes: output.Attributes,
		Batch: &SourceBatchIdentity{
			Kind: SourceBatchPluginOutput, ID: batch.InstanceID.String() + ":" + strconv.FormatUint(batch.Sequence, 10), Sequence: batch.Sequence,
		},
	}
	if sample.SchemaVersion != descriptor.SchemaVersion || sample.DataType != descriptor.DataType {
		sample.Value = nil
		sample.Quality = SourceQualityBad
		sample.Error = "Plugin output schema does not match the current descriptor"
	}
	sample.CoveragePercent = cloneFloat(output.CoveragePercent)
	sample.Attributes = cloneAttributes(output.Attributes)
	sample.Issues = make([]SourceIssue, len(output.Issues))
	for index, issue := range output.Issues {
		sample.Issues[index] = SourceIssue{
			Code: issue.Code, Message: issue.Message, Source: issue.Source,
			PeriodStart: cloneTime(issue.PeriodStart), PeriodEnd: cloneTime(issue.PeriodEnd),
		}
	}
	return sample
}

func unavailableSample(descriptor SourceDescriptor, alias string) SourceSample {
	return SourceSample{
		Alias: alias, Reference: descriptor.Reference, SchemaVersion: descriptor.SchemaVersion,
		DataType: descriptor.DataType, Unit: descriptor.Unit, Quality: SourceQualityUnavailable,
		Error: "source value is not available",
	}
}

func descriptorFromTag(entity tag.Tag) (SourceDescriptor, error) {
	dataType := SourceDataType(entity.DataType)
	if !validSourceDataType(dataType) || entity.ID == uuid.Nil {
		return SourceDescriptor{}, ErrInvalidSourceReference
	}
	description := ""
	if entity.Description != nil {
		description = *entity.Description
	}
	return SourceDescriptor{
		Reference: TagSource(entity.ID), Name: entity.Name, Description: description,
		SchemaVersion: 1, DataType: dataType, PeriodKind: SourcePeriodInstantaneous, Enabled: entity.Enabled,
	}, nil
}

func descriptorFromPluginOutput(instance plugin.Instance, output plugin.OutputDescriptor) (SourceDescriptor, error) {
	reference := PluginOutputSource(instance.ID, string(output.Key))
	dataType := SourceDataType(output.DataType)
	periodKind := SourcePeriodKind(output.PeriodKind)
	if err := reference.Validate(); err != nil || !validSourceDataType(dataType) || !validSourcePeriodKind(periodKind) {
		return SourceDescriptor{}, ErrInvalidSourceReference
	}
	return SourceDescriptor{
		Reference: reference, Name: output.Name, OwnerName: instance.Name, Description: output.Description,
		SchemaVersion: output.SchemaVersion, DataType: dataType, Unit: output.Unit,
		DynamicUnit: output.DynamicUnit, PeriodKind: periodKind, Enabled: instance.Enabled,
	}, nil
}

func currentFromSample(sample SourceSample) SourceCurrent {
	return SourceCurrent{Quality: sample.Quality, Sequence: sample.Sequence, ObservedAt: cloneTime(sample.ObservedAt)}
}

func latestTagValue(values TagSourceValueFeed, tagID uuid.UUID) (tag.TagValue, bool) {
	return values.Latest(tagID)
}

func matchesCatalogSearch(descriptor SourceDescriptor, search string) bool {
	if search == "" {
		return true
	}
	haystack := strings.ToLower(strings.Join([]string{
		descriptor.Name, descriptor.OwnerName, descriptor.Description,
		descriptor.Reference.String(), descriptor.Reference.OutputKey,
	}, " "))
	return strings.Contains(haystack, search)
}

func sourceLookupError(reference SourceReference, err error) error {
	if errors.Is(err, tag.ErrTagNotFound) || errors.Is(err, plugin.ErrInstanceNotFound) {
		return fmt.Errorf("%w: %s", ErrSourceNotFound, reference)
	}
	return err
}

func validSourceDataType(dataType SourceDataType) bool {
	switch dataType {
	case SourceDataTypeBool, SourceDataTypeInt16, SourceDataTypeUInt16, SourceDataTypeInt32,
		SourceDataTypeUInt32, SourceDataTypeFloat32, SourceDataTypeFloat64, SourceDataTypeString:
		return true
	default:
		return false
	}
}

func validSourcePeriodKind(periodKind SourcePeriodKind) bool {
	return periodKind == SourcePeriodInstantaneous || periodKind == SourcePeriodWindowed
}

func cloneFloat(value *float64) *float64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneAttributes(attributes map[string]string) map[string]string {
	if attributes == nil {
		return nil
	}
	cloned := make(map[string]string, len(attributes))
	for key, value := range attributes {
		cloned[key] = value
	}
	return cloned
}

func isNilSourceDependency(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

type sourceCursor struct {
	available bool
	sequence  uint64
}

type sourceSubscription struct {
	ctx       context.Context
	cancel    context.CancelFunc
	events    chan SourceSample
	closers   []func()
	waitGroup sync.WaitGroup
	closeOnce sync.Once

	mu   sync.Mutex
	err  error
	seen map[SourceReference]sourceCursor
}

func (subscription *sourceSubscription) Events() <-chan SourceSample {
	if subscription == nil {
		return nil
	}
	return subscription.events
}

func (subscription *sourceSubscription) Err() error {
	if subscription == nil {
		return nil
	}
	subscription.mu.Lock()
	defer subscription.mu.Unlock()
	return subscription.err
}

func (subscription *sourceSubscription) Close() {
	if subscription == nil {
		return
	}
	subscription.closeUpstreams()
}

func (subscription *sourceSubscription) closeUpstreams() {
	subscription.closeOnce.Do(func() {
		subscription.cancel()
		for _, closeUpstream := range subscription.closers {
			if closeUpstream != nil {
				closeUpstream()
			}
		}
	})
}

func (subscription *sourceSubscription) finish() {
	subscription.waitGroup.Wait()
	subscription.closeUpstreams()
	close(subscription.events)
}

func (subscription *sourceSubscription) fail(err error) {
	subscription.mu.Lock()
	if subscription.err == nil {
		subscription.err = err
	}
	subscription.mu.Unlock()
	subscription.closeUpstreams()
}

func (subscription *sourceSubscription) publish(sample SourceSample) {
	subscription.mu.Lock()
	cursor, exists := subscription.seen[sample.Reference]
	if exists && ((!sample.Available && !cursor.available) || sample.Available && cursor.available && sample.Sequence <= cursor.sequence) {
		subscription.mu.Unlock()
		return
	}
	select {
	case subscription.events <- cloneSourceSample(sample):
		subscription.seen[sample.Reference] = sourceCursor{available: sample.Available, sequence: sample.Sequence}
		subscription.mu.Unlock()
	default:
		if subscription.err == nil {
			subscription.err = ErrSourceSubscriberLag
		}
		subscription.mu.Unlock()
		subscription.closeUpstreams()
	}
}

func (subscription *sourceSubscription) runTagStream(stream <-chan tag.TagValue, sources map[uuid.UUID]ResolvedSource, feed *SourceFeed) {
	defer subscription.waitGroup.Done()
	for {
		select {
		case <-subscription.ctx.Done():
			return
		case value, open := <-stream:
			if !open {
				if subscription.ctx.Err() == nil {
					subscription.fail(ErrSourceUpstreamClosed)
				}
				return
			}
			source, exists := sources[value.TagID]
			if exists {
				subscription.publish(feed.sampleForTag(source.Descriptor, source.Alias, value, true))
			}
		}
	}
}

func (subscription *sourceSubscription) runPluginStream(upstream plugin.OutputSubscription, sources []ResolvedSource) {
	defer subscription.waitGroup.Done()
	for {
		select {
		case <-subscription.ctx.Done():
			return
		case batch, open := <-upstream.Events():
			if !open {
				if subscription.ctx.Err() == nil {
					err := upstream.Err()
					if err == nil {
						err = ErrSourceUpstreamClosed
					}
					subscription.fail(err)
				}
				return
			}
			for _, source := range sources {
				if sample := sampleForPluginOutput(source.Descriptor, source.Alias, &batch); sample.Available {
					subscription.publish(sample)
				}
			}
		}
	}
}

var _ UnifiedSourceReader = (*SourceFeed)(nil)
