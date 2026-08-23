package pluginpostgres

import (
	"context"
	"errors"
	"os"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/plugin"
	"gorm.io/gorm"
)

func TestOutputRepositoryPersistsAtomicLatestBatchAndPreservesTypes_Integration(t *testing.T) {
	database := newPluginOutputRepositoryDatabase(t)
	instances := NewRepository(database)
	repository := NewOutputRepository(database)
	ctx := context.Background()
	instance := plugin.Instance{Type: "energy_management", Name: "Output Test", Enabled: true, Config: plugin.Config(`{}`), ConfigVersion: 1}
	if err := instances.Create(ctx, &instance); err != nil {
		t.Fatalf("creating Plugin instance: %v", err)
	}
	at := time.Date(2026, time.August, 23, 10, 0, 0, 0, time.UTC)
	issueStart, issueEnd := at.Add(-50*time.Minute), at.Add(-40*time.Minute)
	coverage := 87.5
	values := []plugin.OutputValue{
		outputRepositoryValue("bool", plugin.OutputDataTypeBool, "", true, at),
		outputRepositoryValue("int16", plugin.OutputDataTypeInt16, "", int16(-16), at),
		outputRepositoryValue("uint16", plugin.OutputDataTypeUInt16, "", uint16(16), at),
		outputRepositoryValue("int32", plugin.OutputDataTypeInt32, "", int32(-32), at),
		outputRepositoryValue("uint32", plugin.OutputDataTypeUInt32, "", uint32(32), at),
		outputRepositoryValue("float32", plugin.OutputDataTypeFloat32, "kW", float32(1.25), at),
		outputRepositoryValue("float64", plugin.OutputDataTypeFloat64, "kWh", 2.5, at),
		outputRepositoryValue("string", plugin.OutputDataTypeString, "", "running", at),
		{
			Key: "cost", SchemaVersion: 2, DataType: plugin.OutputDataTypeFloat64, Unit: "THB", Value: 42.5,
			Quality: plugin.OutputQualityPartial, ObservedAt: at, PeriodStart: at.Add(-time.Hour), PeriodEnd: at,
			CoveragePercent: &coverage,
			Issues:          []plugin.OutputIssue{{Code: "gap", Message: "History gap", Source: "logger", PeriodStart: &issueStart, PeriodEnd: &issueEnd}},
			Attributes:      map[string]string{"currency": "THB", "timezone": "Asia/Bangkok"},
		},
	}
	first := plugin.OutputBatch{InstanceID: instance.ID, Sequence: 99, PublishedAt: at.Add(time.Second), Values: values}
	stored, accepted, err := repository.StoreLatest(ctx, first)
	if err != nil || !accepted {
		t.Fatalf("StoreLatest(first) = %#v, %t, %v", stored, accepted, err)
	}
	if stored.Sequence != 1 || stored.InstanceID != instance.ID || stored.PublishedAt.Location() != time.UTC {
		t.Fatalf("stored first = %#v", stored)
	}
	coverage = 1
	first.Values[8].Attributes["currency"] = "USD"
	first.Values[8].Issues[0].Message = "mutated"

	latest, err := repository.Latest(ctx, instance.ID)
	if err != nil {
		t.Fatalf("Latest(first) error = %v", err)
	}
	if latest.Sequence != 1 || len(latest.Values) != len(values) || latest.Values[8].Attributes["currency"] != "THB" || latest.Values[8].Issues[0].Message != "History gap" || *latest.Values[8].CoveragePercent != 87.5 {
		t.Fatalf("latest first = %#v", latest)
	}
	for index, expected := range values[:8] {
		if reflect.TypeOf(latest.Values[index].Value) != reflect.TypeOf(expected.Value) || !reflect.DeepEqual(latest.Values[index].Value, expected.Value) {
			t.Errorf("latest %s = %#v (%T), want %#v (%T)", expected.Key, latest.Values[index].Value, latest.Values[index].Value, expected.Value, expected.Value)
		}
	}
	latest.Values[8].Attributes["currency"] = "changed"
	reloaded, _ := repository.Latest(ctx, instance.ID)
	if reloaded.Values[8].Attributes["currency"] != "THB" {
		t.Fatal("Latest() aliases decoded state")
	}

	if duplicate, accepted, err := repository.StoreLatest(ctx, first); err != nil || accepted || duplicate != nil {
		t.Fatalf("StoreLatest(duplicate) = %#v, %t, %v", duplicate, accepted, err)
	}
	newerAt := at.Add(time.Minute)
	second := plugin.OutputBatch{
		InstanceID: instance.ID, Sequence: 1, PublishedAt: newerAt.Add(time.Second),
		Values: []plugin.OutputValue{outputRepositoryValue("replacement", plugin.OutputDataTypeFloat64, "kW", 88.25, newerAt)},
	}
	stored, accepted, err = repository.StoreLatest(ctx, second)
	if err != nil || !accepted || stored.Sequence != 2 {
		t.Fatalf("StoreLatest(second) = %#v, %t, %v", stored, accepted, err)
	}
	latest, err = repository.Latest(ctx, instance.ID)
	if err != nil || latest.Sequence != 2 || len(latest.Values) != 1 || latest.Values[0].Key != "replacement" || latest.Values[0].Value != 88.25 {
		t.Fatalf("atomic replacement latest = %#v, %v", latest, err)
	}
	regressing := second
	regressing.PublishedAt = newerAt.Add(2 * time.Second)
	regressing.Values = []plugin.OutputValue{outputRepositoryValue("replacement", plugin.OutputDataTypeFloat64, "kW", 1.0, at.Add(30*time.Second))}
	if stored, accepted, err := repository.StoreLatest(ctx, regressing); err != nil || accepted || stored != nil {
		t.Fatalf("StoreLatest(regressing) = %#v, %t, %v", stored, accepted, err)
	}

	if err := instances.Delete(ctx, instance.ID); err != nil {
		t.Fatalf("deleting Plugin instance: %v", err)
	}
	if _, err := repository.Latest(ctx, instance.ID); !errors.Is(err, plugin.ErrOutputBatchNotFound) {
		t.Errorf("Latest(after cascade) error = %v", err)
	}
}

func TestOutputRepositoryRejectsInvalidInputAndHonorsContext_Integration(t *testing.T) {
	database := newPluginOutputRepositoryDatabase(t)
	repository := NewOutputRepository(database)
	at := time.Now().UTC()
	valid := plugin.OutputBatch{InstanceID: uuid.New(), Sequence: 1, PublishedAt: at.Add(time.Second), Values: []plugin.OutputValue{outputRepositoryValue("metric", plugin.OutputDataTypeFloat64, "kW", 1.0, at)}}
	for _, test := range []struct {
		name  string
		ctx   context.Context
		batch plugin.OutputBatch
	}{
		{name: "nil context", batch: valid},
		{name: "nil instance", ctx: context.Background(), batch: func() plugin.OutputBatch { value := valid; value.InstanceID = uuid.Nil; return value }()},
		{name: "zero sequence", ctx: context.Background(), batch: func() plugin.OutputBatch { value := valid; value.Sequence = 0; return value }()},
		{name: "zero published", ctx: context.Background(), batch: func() plugin.OutputBatch { value := valid; value.PublishedAt = time.Time{}; return value }()},
		{name: "empty values", ctx: context.Background(), batch: func() plugin.OutputBatch { value := valid; value.Values = nil; return value }()},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, _, err := repository.StoreLatest(test.ctx, test.batch); !errors.Is(err, plugin.ErrInvalidOutputBatch) {
				t.Errorf("StoreLatest() error = %v", err)
			}
		})
	}
	if _, err := repository.Latest(nil, uuid.New()); !errors.Is(err, plugin.ErrInvalidInput) {
		t.Errorf("Latest(nil context) error = %v", err)
	}
	if _, err := repository.Latest(context.Background(), uuid.Nil); !errors.Is(err, plugin.ErrInvalidInput) {
		t.Errorf("Latest(nil ID) error = %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := repository.Latest(cancelled, uuid.New()); !errors.Is(err, context.Canceled) {
		t.Errorf("Latest(cancelled) error = %v", err)
	}
	if _, _, err := repository.StoreLatest(context.Background(), valid); err == nil {
		t.Error("StoreLatest(missing Plugin) succeeded")
	}
}

func TestOutputRepositorySerializesConcurrentMonotonicUpserts_Integration(t *testing.T) {
	database := newPluginOutputRepositoryDatabase(t)
	instances := NewRepository(database)
	repository := NewOutputRepository(database)
	instance := plugin.Instance{Type: "energy_management", Name: "Concurrent Output", Config: plugin.Config(`{}`), ConfigVersion: 1}
	if err := instances.Create(context.Background(), &instance); err != nil {
		t.Fatalf("creating Plugin instance: %v", err)
	}
	start := time.Date(2026, time.August, 23, 11, 0, 0, 0, time.UTC)
	const writerCount = 32
	type result struct {
		batch    *plugin.OutputBatch
		accepted bool
		err      error
	}
	results := make(chan result, writerCount)
	var waitGroup sync.WaitGroup
	for index := range writerCount {
		waitGroup.Add(1)
		go func(index int) {
			defer waitGroup.Done()
			observedAt := start.Add(time.Duration(index) * time.Second)
			batch := plugin.OutputBatch{
				InstanceID: instance.ID, Sequence: 1, PublishedAt: start.Add(time.Hour),
				Values: []plugin.OutputValue{outputRepositoryValue("metric", plugin.OutputDataTypeFloat64, "kW", float64(index), observedAt)},
			}
			stored, accepted, err := repository.StoreLatest(context.Background(), batch)
			results <- result{batch: stored, accepted: accepted, err: err}
		}(index)
	}
	waitGroup.Wait()
	close(results)
	acceptedSequences := make(map[uint64]struct{})
	for result := range results {
		if result.err != nil {
			t.Fatalf("concurrent StoreLatest() error = %v", result.err)
		}
		if !result.accepted {
			continue
		}
		if result.batch == nil {
			t.Fatal("accepted concurrent StoreLatest() returned nil batch")
		}
		if _, duplicate := acceptedSequences[result.batch.Sequence]; duplicate {
			t.Errorf("duplicate accepted sequence %d", result.batch.Sequence)
		}
		acceptedSequences[result.batch.Sequence] = struct{}{}
	}
	latest, err := repository.Latest(context.Background(), instance.ID)
	if err != nil {
		t.Fatalf("Latest() error = %v", err)
	}
	wantObservedAt := start.Add((writerCount - 1) * time.Second)
	if !latest.Values[0].ObservedAt.Equal(wantObservedAt) || latest.Values[0].Value != float64(writerCount-1) || latest.Sequence != uint64(len(acceptedSequences)) {
		t.Errorf("concurrent latest = %#v, accepted sequences %d", latest, len(acceptedSequences))
	}
}

func newPluginOutputRepositoryDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	database := newPluginRepositoryDatabase(t)
	migration, err := os.ReadFile("../../../migrations/000011_create_plugin_output_latest.up.sql")
	if err != nil {
		t.Fatalf("reading Plugin output migration: %v", err)
	}
	if err := database.Exec(string(migration)).Error; err != nil {
		t.Fatalf("applying Plugin output migration: %v", err)
	}
	return database
}

func outputRepositoryValue(key plugin.OutputKey, dataType plugin.OutputDataType, unit string, value any, at time.Time) plugin.OutputValue {
	return plugin.OutputValue{
		Key: key, SchemaVersion: 1, DataType: dataType, Unit: unit, Value: value,
		Quality: plugin.OutputQualityGood, ObservedAt: at, PeriodStart: at, PeriodEnd: at,
	}
}
