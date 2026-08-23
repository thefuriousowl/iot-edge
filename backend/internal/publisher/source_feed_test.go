package publisher

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/plugin"
	"github.com/thefuriousowl/iot-edge/internal/tag"
)

func TestNewSourceFeedValidatesDependenciesAndOptions(t *testing.T) {
	t.Parallel()

	tagCatalog := &sourceTestTagCatalog{}
	tagValues := tag.NewMemoryValueStore()
	pluginCatalog := &sourceTestPluginCatalog{}
	pluginValues := newSourceTestPluginFeed(t)
	var nilTagCatalog *sourceTestTagCatalog
	var nilTagValues *tag.MemoryValueStore
	var nilPluginCatalog *sourceTestPluginCatalog
	var nilPluginValues *sourceTestPluginFeed
	tests := []struct {
		name         string
		tags         TagSourceCatalog
		tagValues    TagSourceValueFeed
		plugins      PluginSourceCatalog
		pluginValues plugin.OutputFeed
		want         error
	}{
		{name: "Tag catalog", tagValues: tagValues, plugins: pluginCatalog, pluginValues: pluginValues, want: ErrTagCatalogRequired},
		{name: "typed nil Tag catalog", tags: nilTagCatalog, tagValues: tagValues, plugins: pluginCatalog, pluginValues: pluginValues, want: ErrTagCatalogRequired},
		{name: "Tag values", tags: tagCatalog, plugins: pluginCatalog, pluginValues: pluginValues, want: ErrTagValueFeedRequired},
		{name: "typed nil Tag values", tags: tagCatalog, tagValues: nilTagValues, plugins: pluginCatalog, pluginValues: pluginValues, want: ErrTagValueFeedRequired},
		{name: "Plugin catalog", tags: tagCatalog, tagValues: tagValues, pluginValues: pluginValues, want: ErrPluginCatalogRequired},
		{name: "typed nil Plugin catalog", tags: tagCatalog, tagValues: tagValues, plugins: nilPluginCatalog, pluginValues: pluginValues, want: ErrPluginCatalogRequired},
		{name: "Plugin values", tags: tagCatalog, tagValues: tagValues, plugins: pluginCatalog, want: ErrPluginFeedRequired},
		{name: "typed nil Plugin values", tags: tagCatalog, tagValues: tagValues, plugins: pluginCatalog, pluginValues: nilPluginValues, want: ErrPluginFeedRequired},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewSourceFeed(test.tags, test.tagValues, test.plugins, test.pluginValues); !errors.Is(err, test.want) {
				t.Fatalf("NewSourceFeed() error = %v, want %v", err, test.want)
			}
		})
	}
	if _, err := NewSourceFeed(tagCatalog, tagValues, pluginCatalog, pluginValues, WithSourceFeedClock(nil)); !errors.Is(err, ErrInvalidCatalogInput) {
		t.Errorf("nil clock error = %v", err)
	}
	if _, err := NewSourceFeed(tagCatalog, tagValues, pluginCatalog, pluginValues, WithSourceEventBuffer(0)); !errors.Is(err, ErrInvalidCatalogInput) {
		t.Errorf("invalid buffer error = %v", err)
	}
}

func TestSourceFeedCatalogCombinesMetadataAndCurrentQuality(t *testing.T) {
	t.Parallel()

	fixture := newSourceFeedFixture(t)
	entries, err := fixture.feed.Catalog(context.Background(), SourceCatalogInput{})
	if err != nil {
		t.Fatalf("Catalog() error = %v", err)
	}
	if len(entries) != 4 {
		t.Fatalf("Catalog() returned %d entries: %#v", len(entries), entries)
	}
	wantNames := []string{"Alarm", "Voltage", "Demand", "Today energy"}
	for index, name := range wantNames {
		if entries[index].Descriptor.Name != name {
			t.Errorf("entry %d name = %q, want %q", index, entries[index].Descriptor.Name, name)
		}
	}
	if entries[0].Descriptor.Reference.Kind != SourceKindTag || entries[2].Descriptor.Reference.Kind != SourceKindPluginOutput {
		t.Fatalf("catalog groups = %#v", entries)
	}
	if entries[0].Current.Quality != SourceQualityUnavailable || entries[0].Current.ObservedAt != nil {
		t.Errorf("missing Tag current = %#v", entries[0].Current)
	}
	if entries[1].Current.Quality != SourceQualityGood || entries[1].Current.Sequence != 4 || !entries[1].Current.ObservedAt.Equal(fixture.observedAt) {
		t.Errorf("Tag current = %#v", entries[1].Current)
	}
	if entries[2].Descriptor.Unit != "kW" || entries[2].Descriptor.PeriodKind != SourcePeriodInstantaneous || entries[2].Current.Quality != SourceQualityGood {
		t.Errorf("demand catalog entry = %#v", entries[2])
	}
	if entries[3].Descriptor.PeriodKind != SourcePeriodWindowed || entries[3].Current.Quality != SourceQualityPartial || entries[3].Current.Sequence != 7 || entries[3].Current.CoveragePercent == nil || *entries[3].Current.CoveragePercent != 80 {
		t.Errorf("energy catalog entry = %#v", entries[3])
	}
	if !entries[3].Current.PeriodStart.Equal(fixture.observedAt.Add(-time.Hour)) || !entries[3].Current.PeriodEnd.Equal(fixture.observedAt) {
		t.Errorf("energy catalog period = %#v", entries[3].Current)
	}
	if calls := fixture.pluginValues.latestCallCount(fixture.instanceID); calls != 1 {
		t.Errorf("Plugin latest calls = %d, want 1 per instance", calls)
	}
	*entries[3].Current.CoveragePercent = 1
	pluginKind := SourceKindPluginOutput
	again, err := fixture.feed.Catalog(context.Background(), SourceCatalogInput{Kind: &pluginKind})
	if err != nil || len(again) != 2 || again[1].Current.CoveragePercent == nil || *again[1].Current.CoveragePercent != 80 {
		t.Fatalf("catalog current aliases Plugin output = %#v, %v", again, err)
	}
	kind := SourceKindPluginOutput
	filtered, err := fixture.feed.Catalog(context.Background(), SourceCatalogInput{Kind: &kind, Search: "today"})
	if err != nil || len(filtered) != 1 || filtered[0].Descriptor.Reference.OutputKey != "today.energy_kwh" {
		t.Fatalf("filtered catalog = %#v, %v", filtered, err)
	}
	disabled := false
	filtered, err = fixture.feed.Catalog(context.Background(), SourceCatalogInput{Enabled: &disabled})
	if err != nil || len(filtered) != 1 || filtered[0].Descriptor.Reference.TagID != fixture.alarmTagID {
		t.Fatalf("disabled catalog = %#v, %v", filtered, err)
	}
	invalidKind := SourceKind("datasource")
	if _, err := fixture.feed.Catalog(context.Background(), SourceCatalogInput{Kind: &invalidKind}); !errors.Is(err, ErrInvalidCatalogInput) {
		t.Errorf("invalid kind error = %v", err)
	}
	if _, err := fixture.feed.Catalog(context.Background(), SourceCatalogInput{Search: string(make([]byte, maxCatalogSearchLength+1))}); !errors.Is(err, ErrInvalidCatalogInput) {
		t.Errorf("oversized search error = %v", err)
	}
	if _, err := (*SourceFeed)(nil).Catalog(context.Background(), SourceCatalogInput{}); !errors.Is(err, ErrInvalidCatalogInput) {
		t.Errorf("nil feed Catalog() error = %v", err)
	}
}

func TestSourceFeedSnapshotPreservesPerSourceProvenanceWithoutFalseSynchronization(t *testing.T) {
	t.Parallel()

	fixture := newSourceFeedFixture(t)
	selections := []SourceSelection{
		{Alias: "energy", Reference: PluginOutputSource(fixture.instanceID, "today.energy_kwh")},
		{Alias: "voltage", Reference: TagSource(fixture.voltageTagID)},
		{Alias: "demand", Reference: PluginOutputSource(fixture.instanceID, "demand_kw")},
	}
	snapshot, err := fixture.feed.Snapshot(context.Background(), selections)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if !snapshot.CapturedAt.Equal(fixture.capturedAt) || len(snapshot.Samples) != 3 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	if snapshot.Samples[0].Alias != "energy" || snapshot.Samples[1].Alias != "voltage" || snapshot.Samples[2].Alias != "demand" {
		t.Fatalf("snapshot order = %#v", snapshot.Samples)
	}
	energy := snapshot.Samples[0]
	voltage := snapshot.Samples[1]
	demand := snapshot.Samples[2]
	if energy.Value != float64(12.5) || energy.Quality != SourceQualityPartial || *energy.CoveragePercent != 80 || energy.Issues[0].Code != "gap" || energy.Attributes["timezone"] != "Asia/Bangkok" {
		t.Errorf("energy sample = %#v", energy)
	}
	if !energy.PeriodStart.Equal(fixture.observedAt.Add(-time.Hour)) || !energy.PeriodEnd.Equal(fixture.observedAt) {
		t.Errorf("energy period = %v - %v", energy.PeriodStart, energy.PeriodEnd)
	}
	if voltage.Value != float64(230.5) || voltage.Unit != "" || voltage.Batch != nil || !voltage.PeriodStart.Equal(fixture.observedAt) || !voltage.PeriodEnd.Equal(fixture.observedAt) {
		t.Errorf("Tag sample = %#v", voltage)
	}
	if demand.Value != float64(42.25) || demand.Batch == nil || energy.Batch == nil || demand.Batch.ID != energy.Batch.ID || demand.Batch.Sequence != 7 {
		t.Errorf("Plugin batch identities = %#v / %#v", demand.Batch, energy.Batch)
	}
	if calls := fixture.pluginValues.latestCallCount(fixture.instanceID); calls != 1 {
		t.Errorf("Snapshot Plugin latest calls = %d, want 1", calls)
	}

	energy.Attributes["timezone"] = "UTC"
	energy.Issues[0].Message = "mutated"
	*energy.CoveragePercent = 1
	again, err := fixture.feed.Latest(context.Background(), selections[0])
	if err != nil || again.Attributes["timezone"] != "Asia/Bangkok" || again.Issues[0].Message != "Missing interval" || *again.CoveragePercent != 80 {
		t.Fatalf("Latest() aliases prior snapshot: %#v, %v", again, err)
	}

	unavailable, err := fixture.feed.Latest(context.Background(), SourceSelection{Alias: "alarm", Reference: TagSource(fixture.alarmTagID)})
	if err != nil || unavailable.Available || unavailable.Quality != SourceQualityUnavailable || unavailable.ObservedAt != nil || unavailable.Sequence != 0 {
		t.Fatalf("unavailable sample = %#v, %v", unavailable, err)
	}

	fixture.tagCatalog.setDataType(fixture.voltageTagID, tag.DataTypeBool)
	mismatch, err := fixture.feed.Latest(context.Background(), SourceSelection{Alias: "voltage", Reference: TagSource(fixture.voltageTagID)})
	if err != nil || !mismatch.Available || mismatch.Quality != SourceQualityBad || mismatch.Value != nil || mismatch.Error == "" {
		t.Fatalf("schema mismatch = %#v, %v", mismatch, err)
	}

	if _, err := fixture.feed.Resolve(context.Background(), []SourceSelection{{Alias: "missing", Reference: TagSource(uuid.New())}}); !errors.Is(err, ErrSourceNotFound) {
		t.Errorf("missing Tag error = %v", err)
	}
	if _, err := fixture.feed.Resolve(context.Background(), []SourceSelection{{Alias: "missing", Reference: PluginOutputSource(fixture.instanceID, "missing")}}); !errors.Is(err, ErrSourceNotFound) {
		t.Errorf("missing output error = %v", err)
	}
}

func TestSourceFeedSubscriptionReplaysAndStreamsBothSourceKinds(t *testing.T) {
	fixture := newSourceFeedFixture(t)
	selections := []SourceSelection{
		{Alias: "voltage", Reference: TagSource(fixture.voltageTagID)},
		{Alias: "demand", Reference: PluginOutputSource(fixture.instanceID, "demand_kw")},
		{Alias: "energy", Reference: PluginOutputSource(fixture.instanceID, "today.energy_kwh")},
	}
	subscription, err := fixture.feed.Subscribe(context.Background(), selections)
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	initial := awaitSourceSamples(t, subscription.Events(), 3)
	for index, alias := range []string{"voltage", "demand", "energy"} {
		if initial[index].Alias != alias {
			t.Fatalf("initial event %d alias = %q, want %q", index, initial[index].Alias, alias)
		}
	}

	fixture.pluginValues.publish(fixture.batch)
	assertNoSourceSample(t, subscription.Events())

	nextAt := fixture.observedAt.Add(time.Second)
	stored, err := fixture.tagValues.Put(tag.TagValue{
		TagID: fixture.voltageTagID, ObservedAt: nextAt, Quality: tag.ValueQualityGood,
		DataType: tag.DataTypeFloat64, Value: float64(231.25),
	})
	if err != nil {
		t.Fatalf("Tag Put() error = %v", err)
	}
	nextBatch := sourceTestOutputBatch(fixture.instanceID, 8, nextAt)
	fixture.pluginValues.publish(nextBatch)
	events := awaitSourceSamples(t, subscription.Events(), 3)
	byAlias := make(map[string]SourceSample, len(events))
	for _, event := range events {
		byAlias[event.Alias] = event
	}
	voltage := byAlias["voltage"]
	demand := byAlias["demand"]
	energy := byAlias["energy"]
	if voltage.Sequence != stored.Sequence || voltage.Batch != nil {
		t.Errorf("Tag event = %#v", voltage)
	}
	if demand.Batch == nil || energy.Batch == nil || demand.Batch.ID != energy.Batch.ID || demand.Batch.Sequence != 8 {
		t.Errorf("Plugin events = %#v / %#v", demand, energy)
	}
	if !voltage.ObservedAt.Equal(*demand.ObservedAt) || voltage.Batch != nil {
		t.Error("equal observation timestamps incorrectly created cross-source synchronization")
	}

	subscription.Close()
	awaitSourceSubscriptionClosed(t, subscription.Events())
	if err := subscription.Err(); err != nil {
		t.Errorf("closed subscription error = %v", err)
	}
}

func TestSourceFeedSubscriptionDisconnectsLaggingConsumer(t *testing.T) {
	fixture := newSourceFeedFixture(t, WithSourceEventBuffer(1))
	subscription, err := fixture.feed.Subscribe(context.Background(), []SourceSelection{{Alias: "voltage", Reference: TagSource(fixture.voltageTagID)}})
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	for index := 0; index < 10; index++ {
		if _, err := fixture.tagValues.Put(tag.TagValue{
			TagID: fixture.voltageTagID, ObservedAt: fixture.observedAt.Add(time.Duration(index+1) * time.Second),
			Quality: tag.ValueQualityGood, DataType: tag.DataTypeFloat64, Value: float64(index),
		}); err != nil {
			t.Fatalf("Tag Put() error = %v", err)
		}
	}
	deadline := time.Now().Add(time.Second)
	for !errors.Is(subscription.Err(), ErrSourceSubscriberLag) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !errors.Is(subscription.Err(), ErrSourceSubscriberLag) {
		t.Fatalf("subscription error = %v", subscription.Err())
	}
	for range subscription.Events() {
	}
}

func TestSourceFeedSubscriptionSurfacesUnexpectedUpstreamClose(t *testing.T) {
	fixture := newSourceFeedFixture(t)
	subscription, err := fixture.feed.Subscribe(context.Background(), []SourceSelection{{Alias: "demand", Reference: PluginOutputSource(fixture.instanceID, "demand_kw")}})
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	_ = awaitSourceSamples(t, subscription.Events(), 1)
	fixture.pluginValues.closeLastSubscription()
	awaitSourceSubscriptionClosed(t, subscription.Events())
	if !errors.Is(subscription.Err(), ErrSourceUpstreamClosed) {
		t.Fatalf("subscription error = %v", subscription.Err())
	}
}

type sourceFeedFixture struct {
	feed         *SourceFeed
	tagCatalog   *sourceTestTagCatalog
	tagValues    *tag.MemoryValueStore
	pluginValues *sourceTestPluginFeed
	voltageTagID uuid.UUID
	alarmTagID   uuid.UUID
	instanceID   uuid.UUID
	observedAt   time.Time
	capturedAt   time.Time
	batch        plugin.OutputBatch
}

func newSourceFeedFixture(t *testing.T, options ...SourceFeedOption) sourceFeedFixture {
	t.Helper()
	observedAt := time.Date(2026, time.August, 23, 5, 0, 0, 0, time.UTC)
	capturedAt := observedAt.Add(2 * time.Second)
	voltageTagID := uuid.New()
	alarmTagID := uuid.New()
	instanceID := uuid.New()
	tagCatalog := &sourceTestTagCatalog{entities: []tag.Tag{
		{ID: voltageTagID, Name: "Voltage", Type: tag.TypeReading, DataType: tag.DataTypeFloat64, Enabled: true},
		{ID: alarmTagID, Name: "Alarm", Type: tag.TypeCalculated, DataType: tag.DataTypeBool, Enabled: false},
	}}
	tagValues := tag.NewMemoryValueStore()
	if err := tagValues.RestoreLatest([]tag.TagValue{{
		TagID: voltageTagID, Sequence: 4, ObservedAt: observedAt, StoredAt: observedAt.Add(time.Second),
		Quality: tag.ValueQualityGood, DataType: tag.DataTypeFloat64, Value: float64(230.5),
	}}); err != nil {
		t.Fatalf("RestoreLatest() error = %v", err)
	}
	pluginCatalog := &sourceTestPluginCatalog{
		instances: []plugin.Instance{{ID: instanceID, Type: "energy_management", Name: "Plant Energy", Enabled: true}},
		outputs: map[uuid.UUID][]plugin.OutputDescriptor{instanceID: {
			{Key: "demand_kw", Name: "Demand", SchemaVersion: 1, DataType: plugin.OutputDataTypeFloat64, Unit: "kW", PeriodKind: plugin.OutputPeriodInstantaneous},
			{Key: "today.energy_kwh", Name: "Today energy", Description: "Today electrical energy", SchemaVersion: 1, DataType: plugin.OutputDataTypeFloat64, Unit: "kWh", PeriodKind: plugin.OutputPeriodWindowed},
		}},
	}
	pluginValues := newSourceTestPluginFeed(t)
	batch := sourceTestOutputBatch(instanceID, 7, observedAt)
	pluginValues.setLatest(batch)
	allOptions := append([]SourceFeedOption{WithSourceFeedClock(func() time.Time { return capturedAt })}, options...)
	feed, err := NewSourceFeed(tagCatalog, tagValues, pluginCatalog, pluginValues, allOptions...)
	if err != nil {
		t.Fatalf("NewSourceFeed() error = %v", err)
	}
	return sourceFeedFixture{
		feed: feed, tagCatalog: tagCatalog, tagValues: tagValues, pluginValues: pluginValues,
		voltageTagID: voltageTagID, alarmTagID: alarmTagID, instanceID: instanceID,
		observedAt: observedAt, capturedAt: capturedAt, batch: batch,
	}
}

func sourceTestOutputBatch(instanceID uuid.UUID, sequence uint64, observedAt time.Time) plugin.OutputBatch {
	coverage := 80.0
	periodStart := observedAt.Add(-time.Hour)
	periodEnd := observedAt
	return plugin.OutputBatch{
		InstanceID: instanceID, Sequence: sequence, PublishedAt: observedAt.Add(time.Second),
		Values: []plugin.OutputValue{
			{
				Key: "demand_kw", SchemaVersion: 1, DataType: plugin.OutputDataTypeFloat64, Unit: "kW",
				Value: float64(42.25), Quality: plugin.OutputQualityGood, ObservedAt: observedAt,
				PeriodStart: observedAt, PeriodEnd: observedAt,
			},
			{
				Key: "today.energy_kwh", SchemaVersion: 1, DataType: plugin.OutputDataTypeFloat64, Unit: "kWh",
				Value: float64(12.5), Quality: plugin.OutputQualityPartial, ObservedAt: observedAt,
				PeriodStart: periodStart, PeriodEnd: periodEnd, CoveragePercent: &coverage,
				Issues:     []plugin.OutputIssue{{Code: "gap", Message: "Missing interval", Source: "electrical", PeriodStart: &periodStart, PeriodEnd: &periodEnd}},
				Attributes: map[string]string{"timezone": "Asia/Bangkok"},
			},
		},
	}
}

type sourceTestTagCatalog struct {
	mu       sync.Mutex
	entities []tag.Tag
	err      error
}

func (catalog *sourceTestTagCatalog) Get(ctx context.Context, id uuid.UUID) (*tag.Tag, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	catalog.mu.Lock()
	defer catalog.mu.Unlock()
	if catalog.err != nil {
		return nil, catalog.err
	}
	for _, entity := range catalog.entities {
		if entity.ID == id {
			cloned := entity
			return &cloned, nil
		}
	}
	return nil, tag.ErrTagNotFound
}

func (catalog *sourceTestTagCatalog) List(ctx context.Context, input tag.ListInput) (*tag.ListResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	catalog.mu.Lock()
	defer catalog.mu.Unlock()
	if catalog.err != nil {
		return nil, catalog.err
	}
	return pagedTags(catalog.entities, input.Page, input.PerPage), nil
}

func (catalog *sourceTestTagCatalog) setDataType(id uuid.UUID, dataType tag.DataType) {
	catalog.mu.Lock()
	defer catalog.mu.Unlock()
	for index := range catalog.entities {
		if catalog.entities[index].ID == id {
			catalog.entities[index].DataType = dataType
		}
	}
}

func pagedTags(entities []tag.Tag, page, perPage int) *tag.ListResult {
	if page < 1 {
		page = 1
	}
	if perPage < 1 {
		perPage = catalogPageSize
	}
	totalPages := (len(entities) + perPage - 1) / perPage
	start := (page - 1) * perPage
	if start >= len(entities) {
		return &tag.ListResult{Data: []tag.Tag{}, Page: page, PerPage: perPage, Total: int64(len(entities)), TotalPages: totalPages}
	}
	end := start + perPage
	if end > len(entities) {
		end = len(entities)
	}
	return &tag.ListResult{Data: append([]tag.Tag(nil), entities[start:end]...), Page: page, PerPage: perPage, Total: int64(len(entities)), TotalPages: totalPages}
}

type sourceTestPluginCatalog struct {
	mu        sync.Mutex
	instances []plugin.Instance
	outputs   map[uuid.UUID][]plugin.OutputDescriptor
	err       error
}

func (catalog *sourceTestPluginCatalog) Get(ctx context.Context, id uuid.UUID) (*plugin.Instance, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	catalog.mu.Lock()
	defer catalog.mu.Unlock()
	if catalog.err != nil {
		return nil, catalog.err
	}
	for _, instance := range catalog.instances {
		if instance.ID == id {
			cloned := instance
			return &cloned, nil
		}
	}
	return nil, plugin.ErrInstanceNotFound
}

func (catalog *sourceTestPluginCatalog) List(ctx context.Context, input plugin.ListInput) (*plugin.ListResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	catalog.mu.Lock()
	defer catalog.mu.Unlock()
	if catalog.err != nil {
		return nil, catalog.err
	}
	page := input.Page
	perPage := input.PerPage
	if page < 1 {
		page = 1
	}
	if perPage < 1 {
		perPage = catalogPageSize
	}
	totalPages := (len(catalog.instances) + perPage - 1) / perPage
	start := (page - 1) * perPage
	if start >= len(catalog.instances) {
		return &plugin.ListResult{Data: []plugin.Instance{}, Page: page, PerPage: perPage, Total: int64(len(catalog.instances)), TotalPages: totalPages}, nil
	}
	end := start + perPage
	if end > len(catalog.instances) {
		end = len(catalog.instances)
	}
	return &plugin.ListResult{Data: append([]plugin.Instance(nil), catalog.instances[start:end]...), Page: page, PerPage: perPage, Total: int64(len(catalog.instances)), TotalPages: totalPages}, nil
}

func (catalog *sourceTestPluginCatalog) OutputDescriptors(ctx context.Context, id uuid.UUID) ([]plugin.OutputDescriptor, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	catalog.mu.Lock()
	defer catalog.mu.Unlock()
	if catalog.err != nil {
		return nil, catalog.err
	}
	outputs, exists := catalog.outputs[id]
	if !exists {
		return nil, plugin.ErrInstanceNotFound
	}
	return append([]plugin.OutputDescriptor(nil), outputs...), nil
}

type sourceTestPluginFeed struct {
	mu            sync.Mutex
	broker        *plugin.OutputBroker
	latest        map[uuid.UUID]plugin.OutputBatch
	latestCalls   map[uuid.UUID]int
	latestErr     map[uuid.UUID]error
	subscriptions []plugin.OutputSubscription
}

func newSourceTestPluginFeed(t *testing.T) *sourceTestPluginFeed {
	t.Helper()
	broker, err := plugin.NewOutputBroker(plugin.WithOutputBuffer(64))
	if err != nil {
		t.Fatalf("NewOutputBroker() error = %v", err)
	}
	return &sourceTestPluginFeed{
		broker: broker, latest: make(map[uuid.UUID]plugin.OutputBatch),
		latestCalls: make(map[uuid.UUID]int), latestErr: make(map[uuid.UUID]error),
	}
}

func (feed *sourceTestPluginFeed) Latest(ctx context.Context, instanceID uuid.UUID) (*plugin.OutputBatch, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	feed.mu.Lock()
	defer feed.mu.Unlock()
	feed.latestCalls[instanceID]++
	if err := feed.latestErr[instanceID]; err != nil {
		return nil, err
	}
	batch, exists := feed.latest[instanceID]
	if !exists {
		return nil, plugin.ErrOutputBatchNotFound
	}
	cloned := plugin.CloneOutputBatch(batch)
	return &cloned, nil
}

func (feed *sourceTestPluginFeed) Subscribe(instanceID uuid.UUID) (plugin.OutputSubscription, error) {
	subscription, err := feed.broker.Subscribe(instanceID)
	if err != nil {
		return nil, err
	}
	feed.mu.Lock()
	feed.subscriptions = append(feed.subscriptions, subscription)
	feed.mu.Unlock()
	return subscription, nil
}

func (feed *sourceTestPluginFeed) setLatest(batch plugin.OutputBatch) {
	feed.mu.Lock()
	feed.latest[batch.InstanceID] = plugin.CloneOutputBatch(batch)
	feed.mu.Unlock()
}

func (feed *sourceTestPluginFeed) publish(batch plugin.OutputBatch) {
	feed.setLatest(batch)
	feed.broker.PublishOutputBatch(batch)
}

func (feed *sourceTestPluginFeed) latestCallCount(instanceID uuid.UUID) int {
	feed.mu.Lock()
	defer feed.mu.Unlock()
	return feed.latestCalls[instanceID]
}

func (feed *sourceTestPluginFeed) closeLastSubscription() {
	feed.mu.Lock()
	defer feed.mu.Unlock()
	if len(feed.subscriptions) > 0 {
		feed.subscriptions[len(feed.subscriptions)-1].Close()
	}
}

func awaitSourceSamples(t *testing.T, events <-chan SourceSample, count int) []SourceSample {
	t.Helper()
	samples := make([]SourceSample, 0, count)
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for len(samples) < count {
		select {
		case sample, open := <-events:
			if !open {
				t.Fatalf("source stream closed after %d/%d samples", len(samples), count)
			}
			samples = append(samples, sample)
		case <-deadline.C:
			t.Fatalf("timed out after %d/%d samples", len(samples), count)
		}
	}
	return samples
}

func assertNoSourceSample(t *testing.T, events <-chan SourceSample) {
	t.Helper()
	select {
	case sample := <-events:
		t.Fatalf("unexpected source sample: %#v", sample)
	case <-time.After(25 * time.Millisecond):
	}
}

func awaitSourceSubscriptionClosed(t *testing.T, events <-chan SourceSample) {
	t.Helper()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for {
		select {
		case _, open := <-events:
			if !open {
				return
			}
		case <-deadline.C:
			t.Fatal("timed out waiting for source subscription to close")
		}
	}
}
